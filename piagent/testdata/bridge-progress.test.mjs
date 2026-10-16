// Fixture-only tests for the Pi bridge's mid-task progress records: a fake Pi
// API and a temporary DevX agent directory (no Pi, tmux or network).
//
//   node --experimental-strip-types --test piagent/testdata/bridge-progress.test.mjs
//
// Invariants: progress is recorded only for the DevX task's own completed
// assistant turns, at most once per interval per task (newest turn kept), is
// never written after the task's outcome is settled, and a failed write
// never affects the task.
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL, fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const BRIDGE = process.env.DEVX_BRIDGE_SOURCE || path.join(here, "..", "bridge", "devx-bridge.ts");
const AGENT = "pa_0123456789ab";
const PANE = "%7";

const real = {};
for (const k of ["writeFileSync", "renameSync", "appendFileSync"]) real[k] = fs[k];
let faults = [];
const inject = (op, match, times = Infinity) => faults.push({ op, match, times });
for (const op of Object.keys(real)) {
	fs[op] = function (...args) {
		const target = String(op === "renameSync" ? args[1] : args[0]).replaceAll("\\", "/");
		const f = faults.find((x) => x.op === op && x.times > 0 && x.match.test(target));
		if (!f) return real[op].apply(fs, args);
		f.times--;
		throw Object.assign(new Error(`injected EIO on ${op} ${path.basename(target)}`), { code: "EIO" });
	};
}
syncBuiltinESMExports();

let seq = 0;
function makeEnv(t) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "dxbp-"));
	const dir = path.join(root, "agents", AGENT);
	fs.mkdirSync(path.join(dir, "tasks"), { recursive: true });
	fs.mkdirSync(path.join(dir, "results"), { recursive: true });
	real.writeFileSync.call(fs, path.join(dir, "agent.json"), JSON.stringify({ id: AGENT, launch_nonce: "n1", binding: { pane_id: PANE }, lease: { holder: "managed", generation: 1 } }));
	t.after(() => {
		faults = [];
		fs.rmSync(root, { recursive: true, force: true });
	});
	return { root, dir };
}
function addTask(dir, n, prompt) {
	const id = `pt_${String(n).padStart(16, "0")}`;
	real.writeFileSync.call(fs, path.join(dir, "tasks", `${id}.json`), JSON.stringify({ id, agent_id: AGENT, order: n, prompt, state: "waiting" }));
	return id;
}
const rj = (f) => JSON.parse(fs.readFileSync(f, "utf8"));
const task = (dir, id) => rj(path.join(dir, "tasks", `${id}.json`));
const progressFile = (dir, id) => path.join(dir, "results", `${id}.progress.json`);
const progress = (dir, id) => (fs.existsSync(progressFile(dir, id)) ? rj(progressFile(dir, id)) : undefined);
const events = (dir) => {
	try {
		return fs.readFileSync(path.join(dir, "events.jsonl"), "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
	} catch {
		return [];
	}
};
const progressEvents = (dir) => events(dir).filter((e) => e.type === "progress");

const assistant = (text, tools = []) => ({
	role: "assistant",
	stopReason: tools.length ? "toolUse" : "stop",
	content: [...(text ? [{ type: "text", text }] : []), ...tools.map((name, i) => ({ type: "toolCall", id: `c${i}`, name, arguments: {} }))],
});

async function startBridge(e, intervalMs) {
	Object.assign(process.env, { DEVX_PI_AGENT_ROOT: e.root, DEVX_PI_AGENT_ID: AGENT, DEVX_PI_LAUNCH_NONCE: "n1", TMUX_PANE: PANE });
	delete process.env.DEVX_PI_TEST_DELIVERY_DELAY_MS;
	delete process.env.DEVX_PI_DEVX_EXECUTABLE;
	if (intervalMs === undefined) delete process.env.DEVX_PI_TEST_PROGRESS_INTERVAL_MS;
	else process.env.DEVX_PI_TEST_PROGRESS_INTERVAL_MS = String(intervalMs);
	const iv = [];
	const o = globalThis.setInterval;
	globalThis.setInterval = (f) => (iv.push(f), { unref() {} });
	const h = {};
	const sent = [];
	const ctx = {
		hasUI: false,
		idle: true,
		pending: false,
		ui: {},
		sessionManager: { getSessionId: () => "s", getSessionFile: () => "" },
		isIdle: () => ctx.idle,
		hasPendingMessages: () => ctx.pending,
		abort() {},
	};
	try {
		const mod = await import(`${pathToFileURL(BRIDGE).href}?i=${++seq}`);
		mod.default({ on: (n, f) => (h[n] = f), registerCommand() {}, sendUserMessage: (t) => sent.push(t) });
		h.session_start({}, ctx);
	} finally {
		globalThis.setInterval = o;
	}
	let turnIndex = 0;
	return {
		h,
		ctx,
		sent,
		tick: () => iv[0](),
		accept: (streamingBehavior) => h.input({ text: sent[sent.length - 1], source: "extension", streamingBehavior }, ctx),
		turn: (msg) => h.turn_end({ type: "turn_end", turnIndex: turnIndex++, message: msg, toolResults: [] }, ctx),
		userStart: (text) => h.message_start({ type: "message_start", message: { role: "user", content: [{ type: "text", text }] } }, ctx),
		finish: (text) => {
			h.agent_end({ messages: [assistant(text)] }, ctx);
			h.agent_settled({}, ctx);
		},
	};
}

async function runningTask(t, intervalMs = 0) {
	const e = makeEnv(t);
	const id = addTask(e.dir, 1, "do the thing");
	const b = await startBridge(e, intervalMs);
	b.tick();
	assert.deepEqual(b.accept(), { action: "continue" });
	b.ctx.idle = false;
	return { e, id, b };
}

test("a completed assistant turn of a running task becomes a progress record and event", async (t) => {
	const { e, id, b } = await runningTask(t);
	b.turn(assistant("Reading the config first.", ["read", "bash"]));
	b.tick();
	const p = progress(e.dir, id);
	assert.equal(p.task_id, id);
	assert.equal(p.turn, 1);
	assert.equal(p.excerpt, "Reading the config first.");
	assert.equal(p.last_tool, "bash", "the last tool called in the turn");
	assert.ok(Date.parse(p.at) > 0);
	const evs = progressEvents(e.dir);
	assert.equal(evs.length, 1);
	assert.equal(evs[0].task_id, id);
	assert.equal(evs[0].source, "bridge");
	assert.deepEqual({ turn: evs[0].data.turn, excerpt: evs[0].data.excerpt, last_tool: evs[0].data.last_tool, at: evs[0].data.at }, { turn: 1, excerpt: p.excerpt, last_tool: "bash", at: p.at });

	b.turn(assistant("Now running the tests."));
	b.tick();
	const p2 = progress(e.dir, id);
	assert.equal(p2.turn, 2);
	assert.equal(p2.excerpt, "Now running the tests.");
	assert.equal(p2.last_tool, undefined, "a turn without tool calls reports none");
	assert.equal(progressEvents(e.dir).length, 2);
	assert.equal(task(e.dir, id).state, "running", "progress never changes the task");
});

test("the raw excerpt is bounded and marked truncated", async (t) => {
	const { e, id, b } = await runningTask(t);
	b.turn(assistant("x".repeat(5000), ["t".repeat(400)]));
	b.tick();
	const p = progress(e.dir, id);
	assert.ok(p.excerpt.length <= 1200 && p.excerpt.length > 500);
	assert.equal(p.excerpt_truncated, true);
	assert.ok(p.last_tool.length <= 128);
});

test("rate limit: chatty turns coalesce into one record per interval, newest kept", async (t) => {
	const { e, id, b } = await runningTask(t, 60000);
	b.turn(assistant("turn one"));
	b.tick();
	assert.equal(progressEvents(e.dir).length, 1, "the first turn is recorded immediately");
	for (let i = 2; i <= 50; i++) {
		b.turn(assistant(`turn ${i}`));
		b.tick();
	}
	assert.equal(progressEvents(e.dir).length, 1, "nothing more within the interval");
	assert.equal(progress(e.dir, id).turn, 1);
});

test("rate limit: the newest pending turn is written once the interval passes", async (t) => {
	const { e, id, b } = await runningTask(t, 150);
	b.turn(assistant("turn one"));
	b.tick();
	b.turn(assistant("turn two"));
	b.turn(assistant("turn three"));
	b.tick();
	assert.equal(progressEvents(e.dir).length, 1);
	await new Promise((r) => setTimeout(r, 200));
	b.tick();
	const evs = progressEvents(e.dir);
	assert.equal(evs.length, 2);
	assert.equal(evs[1].data.turn, 3, "intermediate turns are coalesced away");
	assert.equal(progress(e.dir, id).excerpt, "turn three");
	b.tick();
	assert.equal(progressEvents(e.dir).length, 2, "nothing pending, nothing written");
});

test("no progress before Pi accepts the task, nor for turns outside a DevX task", async (t) => {
	const e = makeEnv(t);
	const b0 = await startBridge(e, 0);
	b0.turn(assistant("a human-driven turn")); // no DevX task
	b0.tick();
	const id = addTask(e.dir, 1, "p");
	b0.tick(); // handed to Pi, not yet accepted
	b0.turn(assistant("still the earlier run"));
	b0.tick();
	assert.equal(progress(e.dir, id), undefined);
	assert.equal(progressEvents(e.dir).length, 0);
});

test("a followUp task records progress only once Pi starts its own user message", async (t) => {
	const e = makeEnv(t);
	const id = addTask(e.dir, 1, "follow-up prompt");
	const b = await startBridge(e, 0);
	b.tick();
	assert.deepEqual(b.accept("followUp"), { action: "continue" });
	assert.equal(task(e.dir, id).accepted_as, "followUp");
	b.ctx.idle = false;
	b.turn(assistant("finishing the human's earlier turn"));
	b.tick();
	assert.equal(progressEvents(e.dir).length, 0, "the earlier run's turn is not this task's progress");
	b.userStart("something else");
	b.turn(assistant("still not ours"));
	b.tick();
	assert.equal(progressEvents(e.dir).length, 0);
	b.userStart("follow-up prompt");
	b.turn(assistant("working on the follow-up"));
	b.tick();
	const evs = progressEvents(e.dir);
	assert.equal(evs.length, 1);
	assert.equal(evs[0].data.turn, 1);
	assert.equal(evs[0].data.excerpt, "working on the follow-up");
});

test("pending progress is dropped when the task settles; the result supersedes it", async (t) => {
	const { e, id, b } = await runningTask(t, 60000);
	b.turn(assistant("first"));
	b.tick();
	b.turn(assistant("second, pending"));
	b.ctx.idle = true;
	b.finish("final answer");
	b.tick();
	b.tick();
	assert.equal(task(e.dir, id).state, "completed");
	assert.equal(progressEvents(e.dir).length, 1);
	assert.equal(progress(e.dir, id).turn, 1);
	const types = events(e.dir).map((x) => x.type);
	assert.ok(types.indexOf("progress") < types.indexOf("task_finished"));
});

test("a failed progress write is retried and never affects the task", async (t) => {
	const { e, id, b } = await runningTask(t, 0);
	inject("renameSync", /\.progress\.json$/, 1);
	b.turn(assistant("first try"));
	b.tick();
	assert.equal(progress(e.dir, id), undefined);
	assert.equal(progressEvents(e.dir).length, 0);
	assert.equal(task(e.dir, id).state, "running");
	b.tick();
	assert.equal(progress(e.dir, id).excerpt, "first try");
	assert.equal(progressEvents(e.dir).length, 1);
	// An event append failure keeps the record (the event is best effort).
	inject("appendFileSync", /events\.jsonl$/, 1);
	b.turn(assistant("second"));
	b.tick();
	assert.equal(progress(e.dir, id).excerpt, "second");
	b.ctx.idle = true;
	b.finish("done");
	assert.equal(task(e.dir, id).state, "completed");
});

test("the bridge heartbeat reports protocol 3", async (t) => {
	const { e } = await runningTask(t);
	assert.equal(rj(path.join(e.dir, "bridge.json")).protocol, 3);
});
