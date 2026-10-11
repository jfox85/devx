// Fixture-only tests for the Pi bridge's control decisions under write
// faults: a fake Pi API and a temporary DevX agent directory (no Pi, tmux or
// network).
//
//   node --experimental-strip-types --test piagent/testdata/bridge-controls.test.mjs
//
// Invariant: a control decision saved durably in the task record is acted on
// by this bridge even when the event annotating it cannot be appended, and a
// mark this bridge cannot honour is withdrawn rather than left claiming
// something that did not happen.
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

// fault: { op, match, mode: "before" (fails) | "after" (lands, then reports failure), times }
const real = {};
for (const k of ["writeFileSync", "renameSync", "appendFileSync"]) real[k] = fs[k];
let faults = [];
const inject = (op, match, opts = {}) => faults.push({ op, match, mode: opts.mode ?? "before", times: opts.times ?? Infinity });
for (const op of Object.keys(real)) {
	fs[op] = function (...args) {
		const target = String(op === "renameSync" ? args[1] : args[0]).replaceAll("\\", "/");
		const f = faults.find((x) => x.op === op && x.times > 0 && x.match.test(target));
		if (!f) return real[op].apply(fs, args);
		f.times--;
		if (f.mode === "after") real[op].apply(fs, args);
		throw Object.assign(new Error(`injected EIO on ${op} ${path.basename(target)}`), { code: "EIO" });
	};
}
syncBuiltinESMExports();

let seq = 0;
function makeEnv(t) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "dxbc-"));
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
const agent = (dir) => rj(path.join(dir, "agent.json"));
const patch = (file, f) => {
	const v = rj(file);
	f(v);
	real.writeFileSync.call(fs, file, JSON.stringify(v));
};
const events = (dir) => {
	try {
		return fs.readFileSync(path.join(dir, "events.jsonl"), "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
	} catch {
		return [];
	}
};

async function startBridge(e) {
	Object.assign(process.env, { DEVX_PI_AGENT_ROOT: e.root, DEVX_PI_AGENT_ID: AGENT, DEVX_PI_LAUNCH_NONCE: "n1", TMUX_PANE: PANE });
	delete process.env.DEVX_PI_TEST_DELIVERY_DELAY_MS;
	delete process.env.DEVX_PI_DEVX_EXECUTABLE;
	const iv = [];
	const o = globalThis.setInterval;
	globalThis.setInterval = (f) => (iv.push(f), { unref() {} });
	const h = {};
	const sent = [];
	const ctx = {
		hasUI: false,
		idle: true,
		pending: false,
		aborted: 0,
		ui: {},
		sessionManager: { getSessionId: () => "s", getSessionFile: () => "" },
		isIdle: () => ctx.idle,
		hasPendingMessages: () => ctx.pending,
		abort: () => ctx.aborted++,
	};
	try {
		const mod = await import(`${pathToFileURL(BRIDGE).href}?i=${++seq}`);
		mod.default({ on: (n, f) => (h[n] = f), registerCommand() {}, sendUserMessage: (t) => sent.push(t) });
		h.session_start({}, ctx);
	} finally {
		globalThis.setInterval = o;
	}
	return {
		h,
		ctx,
		sent,
		tick: () => iv[0](),
		accept: () => h.input({ text: sent[sent.length - 1], source: "extension" }, ctx),
		finish: (text, stop = "stop") => {
			h.agent_end({ messages: [{ role: "assistant", stopReason: stop, content: [{ type: "text", text }] }] }, ctx);
			h.agent_settled({}, ctx);
		},
	};
}

// A running, accepted task with a remote cancel requested at the current generation.
async function acceptedWithCancel(t) {
	const e = makeEnv(t);
	const id = addTask(e.dir, 1, "first");
	const b = await startBridge(e);
	b.tick();
	assert.deepEqual(b.accept(), { action: "continue" });
	patch(path.join(e.dir, "tasks", `${id}.json`), (x) => ((x.cancel_requested = true), (x.cancel_lease_generation = 1)));
	b.ctx.idle = false;
	return { e, id, b };
}

for (const [label, mode] of [["fails", "before"], ["lands but reports failure", "after"]]) {
	test(`abort_sent event append ${label}: the saved abort is dispatched exactly once`, async (t) => {
		const { e, id, b } = await acceptedWithCancel(t);
		inject("appendFileSync", /events\.jsonl$/, { mode, times: 1 });
		b.tick();
		faults = [];
		b.tick();
		b.tick();
		assert.equal(b.ctx.aborted, 1, "abort_sent_at is durable, so ctx.abort() is called, once");
		assert.ok(task(e.dir, id).abort_sent_at);
		b.ctx.idle = true;
		b.finish("partial", "aborted");
		assert.equal(task(e.dir, id).note, "aborted on request");
		assert.equal(agent(e.dir).lease.holder, "managed");
	});
}

test("abort_sent_at save lands but reports failure: the owed abort is dispatched once on the next tick", async (t) => {
	const { e, id, b } = await acceptedWithCancel(t);
	inject("renameSync", /tasks\/pt_.*\.json$/, { mode: "after", times: 1 });
	b.tick();
	assert.equal(b.ctx.aborted, 0, "the save reported failure; nothing dispatched yet");
	faults = [];
	b.tick();
	b.tick();
	assert.equal(b.ctx.aborted, 1);
	assert.ok(task(e.dir, id).abort_sent_at);
});

test("an owed abort is re-gated: control moved to a human, so it is withheld and the sent mark withdrawn", async (t) => {
	const { e, id, b } = await acceptedWithCancel(t);
	inject("renameSync", /tasks\/pt_.*\.json$/, { mode: "after", times: 1 });
	b.tick();
	faults = [];
	patch(path.join(e.dir, "agent.json"), (a) => (a.lease = { holder: "human", generation: 2 }));
	b.tick();
	assert.equal(b.ctx.aborted, 0, "never abort under human control");
	const after = task(e.dir, id);
	assert.equal(after.abort_withheld, "human_control");
	assert.ok(!after.abort_sent_at, "an abort never dispatched is not reported as sent");
	assert.ok(events(e.dir).some((x) => x.type === "abort_withheld" && x.data?.abort_not_dispatched));
});

test("an owed abort for a turn that already ended is withdrawn, and a human interrupt is attributed to the human", async (t) => {
	const { e, id, b } = await acceptedWithCancel(t);
	inject("renameSync", /tasks\/pt_.*\.json$/, { mode: "after", times: 1 });
	b.tick();
	faults = [];
	b.ctx.idle = true;
	b.finish("partial", "aborted"); // the human pressed Esc before the owed abort went out
	const after = task(e.dir, id);
	assert.equal(b.ctx.aborted, 0);
	assert.equal(after.note, "interrupted by a human in Pi");
	assert.ok(!after.abort_sent_at);
	assert.equal(agent(e.dir).lease.holder, "human");
});

for (const [label, mode] of [["fails", "before"], ["lands but reports failure", "after"]]) {
	test(`task_started event append ${label}: the accepted prompt runs and is published`, async (t) => {
		const e = makeEnv(t);
		const id = addTask(e.dir, 1, "first");
		const b = await startBridge(e);
		b.tick();
		inject("appendFileSync", /events\.jsonl$/, { mode, times: 1 });
		assert.deepEqual(b.accept(), { action: "continue" }, "a durably accepted prompt reaches Pi");
		faults = [];
		assert.ok(task(e.dir, id).confirmed_at);
		b.finish("done");
		assert.equal(task(e.dir, id).state, "completed");
	});
}

test("acceptance save lands but reports failure: Pi does not get it and the record does not claim it was accepted", async (t) => {
	const e = makeEnv(t);
	const id = addTask(e.dir, 1, "first");
	const b = await startBridge(e);
	b.tick();
	inject("renameSync", /tasks\/pt_.*\.json$/, { mode: "after", times: 1 });
	assert.deepEqual(b.accept(), { action: "handled" }, "fenced: not injected");
	faults = [];
	b.tick(); // puts it back (delivery_fenced) and, Pi being idle, redelivers it
	const u = task(e.dir, id);
	assert.ok(!u.confirmed_at && !u.accepted_as, `stale acceptance left behind: ${JSON.stringify(u)}`);
	assert.ok(events(e.dir).some((x) => x.type === "delivery_fenced" && x.task_id === id));
	assert.equal(b.sent.length, 2, "redelivered once");
	assert.deepEqual(b.accept(), { action: "continue" });
	b.finish("done");
	assert.equal(task(e.dir, id).state, "completed");
	assert.equal(b.sent.length, 2);
});

test("annotation failures elsewhere never stop the bridge acting on durable state (abort_withheld)", async (t) => {
	const { e, id, b } = await acceptedWithCancel(t);
	patch(path.join(e.dir, "agent.json"), (a) => (a.lease = { holder: "human", generation: 2 }));
	inject("appendFileSync", /events\.jsonl$/, { times: 1 });
	b.tick();
	faults = [];
	b.tick();
	assert.equal(b.ctx.aborted, 0);
	assert.equal(task(e.dir, id).abort_withheld, "human_control");
});
