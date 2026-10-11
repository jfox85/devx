// Fault-injection tests for the Pi bridge's durability rules (fixture only:
// a fake Pi API and a temporary DevX agent directory; no Pi, tmux or network).
//
//   node --experimental-strip-types --test piagent/testdata/bridge-durability.test.mjs
//
// DEVX_BRIDGE_SOURCE selects the bridge file (default: the repo's bridge).
// Each scenario loads a fresh bridge module instance.
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

// ---- fault injection over node:fs (seen by the bridge's `import * as fs`) ----
const real = {};
for (const k of ["mkdirSync", "writeFileSync", "renameSync", "appendFileSync", "readFileSync"]) real[k] = fs[k];
let faults = [];
// fault: { op, match: RegExp, mode: "before" | "after", times, code }
function inject(op, match, opts = {}) {
	const f = { op, match, mode: opts.mode ?? "before", times: opts.times ?? Infinity, code: opts.code ?? "EIO", hits: 0 };
	faults.push(f);
	return f;
}
function clearFaults() {
	faults = [];
}
function wrap(op) {
	return function (...args) {
		const target = String(op === "renameSync" ? args[1] : args[0]).replaceAll("\\", "/");
		const f = faults.find((x) => x.op === op && x.times > 0 && x.match.test(target));
		if (!f) return real[op].apply(fs, args);
		f.times--;
		f.hits++;
		const err = Object.assign(new Error(`injected ${f.code} on ${op} ${path.basename(target)}`), { code: f.code });
		if (f.mode === "after") {
			real[op].apply(fs, args); // the write took effect; the caller sees a failure
		}
		throw err;
	};
}
for (const k of Object.keys(real)) fs[k] = wrap(k);
syncBuiltinESMExports();

// ---- controllable clock (backoff) ----
// Re-based per test (in withEnv) so offsets never leak between tests. It
// still advances with real time: the bridge's lock deadline loop needs a
// moving clock. Backoff windows are only asserted right after an attempt.
const realNow = Date.now.bind(Date);
let clockBase = 0;
let clockOffset = 0;
Date.now = () => realNow() + clockBase + clockOffset;
const advance = (ms) => {
	clockOffset += ms;
};

// ---- fixture agent directory ----
let seq = 0;
function makeRoot() {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "dxbd-"));
	const dir = path.join(root, "agents", AGENT);
	fs.mkdirSync(path.join(dir, "tasks"), { recursive: true });
	fs.mkdirSync(path.join(dir, "results"), { recursive: true });
	const agent = { id: AGENT, launch_nonce: "nonce-1", binding: { pane_id: PANE }, lease: { holder: "managed", generation: 1 } };
	real.writeFileSync.call(fs, path.join(dir, "agent.json"), JSON.stringify(agent));
	return { root, dir };
}
function addTask(dir, n, prompt) {
	const id = `pt_${String(n).padStart(16, "0")}`;
	real.writeFileSync.call(fs, path.join(dir, "tasks", `${id}.json`), JSON.stringify({ id, agent_id: AGENT, order: n, prompt, state: "waiting" }));
	return id;
}
const readJ = (f) => JSON.parse(real.readFileSync.call(fs, f, "utf8"));
const task = (dir, id) => readJ(path.join(dir, "tasks", `${id}.json`));
const agent = (dir) => readJ(path.join(dir, "agent.json"));
function events(dir) {
	let s = "";
	try {
		s = real.readFileSync.call(fs, path.join(dir, "events.jsonl"), "utf8");
	} catch {}
	return s
		.split("\n")
		.filter(Boolean)
		.map((l) => JSON.parse(l));
}

// ---- fake Pi ----
async function startBridge(env, opts = {}) {
	process.env.DEVX_PI_AGENT_ROOT = env.root;
	process.env.DEVX_PI_AGENT_ID = AGENT;
	process.env.DEVX_PI_LAUNCH_NONCE = opts.nonce ?? "nonce-1";
	process.env.TMUX_PANE = PANE;
	delete process.env.DEVX_PI_DEVX_EXECUTABLE;
	delete process.env.DEVX_PI_DEVX_SESSION;
	delete process.env.DEVX_PI_TEST_DELIVERY_DELAY_MS;
	const intervals = [];
	const origSetInterval = globalThis.setInterval;
	globalThis.setInterval = (fn) => {
		intervals.push(fn);
		return { unref() {} };
	};
	let mod;
	try {
		mod = await import(`${pathToFileURL(BRIDGE).href}?i=${++seq}`);
	} finally {
		globalThis.setInterval = origSetInterval;
	}
	const handlers = {};
	const commands = {};
	const sent = [];
	const ui = { editor: opts.editor ?? "", status: "", notes: [] };
	const ctx = {
		hasUI: true,
		mode: "tui",
		idle: true,
		pending: false,
		aborted: 0,
		ui: {
			setStatus: (_k, t) => (ui.status = t),
			notify: (m, l) => ui.notes.push({ m, l }),
			getEditorText: () => ui.editor,
			setEditorText: (t) => (ui.editor = t),
		},
		sessionManager: { getSessionId: () => "sess-1", getSessionFile: () => "/tmp/sess-1.jsonl" },
		isIdle: () => ctx.idle,
		hasPendingMessages: () => ctx.pending,
		abort: () => ctx.aborted++,
	};
	const pi = {
		on: (n, f) => (handlers[n] = f),
		registerCommand: (n, c) => (commands[n] = c),
		sendUserMessage: (text) => sent.push(text),
	};
	globalThis.setInterval = (fn) => {
		intervals.push(fn);
		return { unref() {} };
	};
	try {
		mod.default(pi);
		handlers.session_start({}, ctx);
	} finally {
		globalThis.setInterval = origSetInterval;
	}
	const b = {
		ctx,
		ui,
		sent,
		handlers,
		commands,
		tick: () => intervals[0](),
		// Pi hands the delivered prompt back through the input pipeline.
		accept: () => handlers.input({ text: sent[sent.length - 1], source: "extension" }, ctx),
		human: (text) => {
			ui.editor = ""; // Pi clears the editor on submit, before input handlers
			return handlers.input({ text, source: "interactive" }, ctx);
		},
		finishTurn: (text, stop = "stop") => {
			handlers.agent_end({ messages: [{ role: "assistant", stopReason: stop, content: [{ type: "text", text }] }] }, ctx);
			handlers.agent_settled({}, ctx);
		},
		shutdown: () => handlers.session_shutdown({}, ctx),
	};
	return b;
}

// Deliver the next waiting task and let Pi accept it.
function deliverAndAccept(b) {
	const before = b.sent.length;
	b.tick();
	assert.equal(b.sent.length, before + 1, "a task should be delivered");
	assert.deepEqual(b.accept(), { action: "continue" });
}

function withEnv(name, fn) {
	test(name, async (t) => {
		clearFaults();
		clockBase = 0;
		clockOffset = 0;
		const env = makeRoot();
		t.after(() => {
			clearFaults();
			fs.rmSync(env.root, { recursive: true, force: true });
		});
		await fn(env);
	});
}

const lockFault = (opts) => inject("mkdirSync", /agent\.lock$/, { code: "EACCES", ...opts });

// ======================= A: human ownership =======================

for (const [label, arm] of [
	["lock unavailable", () => lockFault()],
	["agent.json write fails", () => inject("writeFileSync", /agent\.json\..*\.tmp$/)],
	["agent.json rename fails", () => inject("renameSync", /agent\.json$/)],
	["agent record unreadable", () => inject("readFileSync", /agent\.json$/, { code: "EIO" })],
]) {
	withEnv(`human input fails closed when takeover is not durable: ${label}`, async (env) => {
		const waiting = addTask(env.dir, 1, "remote work");
		const b = await startBridge(env, { editor: "" });
		b.ui.editor = "";
		arm();
		b.ui.editor = "";
		const res = (() => {
			b.ui.editor = "";
			// typed-after-submit draft must be kept behind the rejected text
			const r = b.handlers.input({ text: "human message", source: "interactive" }, (() => {
				b.ui.editor = "draft2";
				return b.ctx;
			})());
			return r;
		})();
		assert.deepEqual(res, { action: "handled" }, "input must not reach Pi");
		assert.equal(b.ui.editor, "human message\ndraft2", "text restored ahead of the newer draft");
		assert.ok(b.ui.notes.some((n) => n.l === "warning" && /could not record/.test(n.m)));
		// Managed dispatch must not proceed while the claim is unsaved, even
		// with an empty editor and an idle Pi.
		b.ui.editor = "";
		b.tick();
		b.tick();
		assert.equal(b.sent.length, 0, "no managed dispatch while human ownership is unsaved");
		assert.equal(task(env.dir, waiting).state, "waiting");
		clearFaults();
		b.tick(); // saves the claim
		assert.equal(agent(env.dir).lease.holder, "human");
		b.tick();
		assert.equal(b.sent.length, 0, "human holds control after the retry");
		assert.equal(task(env.dir, waiting).state, "waiting");
		// Retrying the submit now works and leaves the lease human.
		assert.deepEqual(b.human("human message"), { action: "continue" });
	});
}

withEnv("once the lease is durably human, failing annotations do not lock the human out", async (env) => {
	const id = addTask(env.dir, 1, "remote work");
	const b = await startBridge(env);
	deliverAndAccept(b);
	inject("appendFileSync", /events\.jsonl$/);
	inject("writeFileSync", /tasks\/pt_.*\.json\..*\.tmp$/);
	assert.deepEqual(b.human("hi"), { action: "continue" });
	assert.deepEqual(b.human("again"), { action: "continue" }, "not locked out on later submits");
	clearFaults();
	assert.equal(agent(env.dir).lease.holder, "human");
	assert.equal(task(env.dir, id).state, "running");
});

withEnv("human input during a running task marks it human_intervened", async (env) => {
	const id = addTask(env.dir, 1, "remote work");
	const b = await startBridge(env);
	deliverAndAccept(b);
	assert.deepEqual(b.human("steer"), { action: "continue" });
	assert.equal(task(env.dir, id).human_intervened, true);
	assert.equal(agent(env.dir).lease.holder, "human");
});

withEnv("human submit while a receipt is pending is accepted and does not touch the settled task", async (env) => {
	const id = addTask(env.dir, 1, "first");
	const b = await startBridge(env);
	deliverAndAccept(b);
	inject("writeFileSync", /results\/pt_.*\.txt\..*\.tmp$/);
	b.finishTurn("answer");
	assert.deepEqual(b.human("new"), { action: "continue" });
	clearFaults();
	advance(60_000);
	b.tick();
	const t = task(env.dir, id);
	assert.equal(t.state, "completed");
	assert.ok(!t.human_intervened);
	assert.equal(agent(env.dir).lease.holder, "human");
	b.tick();
	assert.equal(b.sent.length, 1, "human control: nothing dispatched");
});

withEnv("human input continues only after the lease is durably human", async (env) => {
	const b = await startBridge(env);
	assert.deepEqual(b.human("hello"), { action: "continue" });
	assert.equal(agent(env.dir).lease.holder, "human");
	const types = events(env.dir).map((e) => e.type);
	assert.ok(types.indexOf("lease_changed") < types.indexOf("human_input"), "lease recorded before human_input");
});

withEnv("a delivery racing an unsaved takeover is refused and requeued", async (env) => {
	const id = addTask(env.dir, 1, "remote work");
	const b = await startBridge(env);
	b.tick(); // picks the task and hands it to Pi
	assert.equal(b.sent.length, 1);
	lockFault();
	assert.deepEqual(b.human("mine"), { action: "handled" });
	clearFaults();
	assert.deepEqual(b.accept(), { action: "handled" }, "remote prompt must not be injected");
	b.tick(); // requeue + save claim
	b.tick();
	assert.equal(task(env.dir, id).state, "waiting");
	assert.equal(agent(env.dir).lease.holder, "human");
	assert.ok(events(env.dir).some((e) => e.type === "delivery_fenced" && e.task_id === id && e.data.reason === "human_control"));
});

withEnv("real lock timeout (held by another process) rejects human input", async (env) => {
	addTask(env.dir, 1, "remote work");
	const b = await startBridge(env);
	real.mkdirSync.call(fs, path.join(env.dir, "agent.lock")); // fresh: not stale
	const t0 = realNow();
	assert.deepEqual(b.human("mine"), { action: "handled" });
	assert.ok(realNow() - t0 >= 4000, "waited for the lock timeout");
	assert.equal(b.ui.editor, "mine");
	fs.rmdirSync(path.join(env.dir, "agent.lock"));
	b.tick();
	b.tick();
	assert.equal(b.sent.length, 0);
	assert.equal(agent(env.dir).lease.holder, "human");
});

// ======================= B: terminal receipts =======================

const publishFaults = [
	["lock unavailable", () => lockFault()],
	["result write fails", () => inject("writeFileSync", /results\/pt_.*\.txt\..*\.tmp$/)],
	["result rename fails", () => inject("renameSync", /results\/pt_.*\.txt$/)],
	["result rename lands but reports failure", () => inject("renameSync", /results\/pt_.*\.txt$/, { mode: "after", times: 1 })],
	["task write fails", () => inject("writeFileSync", /tasks\/pt_.*\.json\..*\.tmp$/)],
	["task rename lands but reports failure", () => inject("renameSync", /tasks\/pt_.*\.json$/, { mode: "after", times: 1 })],
	["task_finished append fails", () => inject("appendFileSync", /events\.jsonl$/)],
	["task_finished append lands but reports failure", () => inject("appendFileSync", /events\.jsonl$/, { mode: "after", times: 1 })],
	["spool and lock both fail", () => (inject("writeFileSync", /receipt\.json\..*\.tmp$/), lockFault())],
];

for (const [label, arm] of publishFaults) {
	withEnv(`settled result is retained and published exactly once: ${label}`, async (env) => {
		const first = addTask(env.dir, 1, "first");
		const second = addTask(env.dir, 2, "second");
		const b = await startBridge(env);
		deliverAndAccept(b);
		arm();
		b.finishTurn("the answer");
		// Whatever the fault, nothing else is dispatched and the task is not
		// reported finished without its result.
		for (let i = 0; i < 3; i++) b.tick();
		assert.equal(b.sent.length, 1, "next task must not be dispatched while the result is unsaved");
		assert.equal(task(env.dir, second).state, "waiting");
		const mid = task(env.dir, first);
		if (mid.state !== "running") {
			// an "after" fault let the task write land: it must be complete
			assert.equal(mid.state, "completed");
			assert.ok(mid.terminal_receipt);
		}
		// A second settle (e.g. a spurious follow-up) must not replace the
		// frozen outcome.
		b.finishTurn("something else");
		clearFaults();
		advance(60_000);
		b.tick();
		const t = task(env.dir, first);
		assert.equal(t.state, "completed");
		assert.equal(real.readFileSync.call(fs, path.join(env.dir, "results", `${first}.txt`), "utf8"), "the answer");
		const fin = events(env.dir).filter((e) => e.type === "task_finished" && e.task_id === first);
		assert.equal(fin.length, 1, "exactly one terminal event");
		assert.equal(fin[0].data.receipt_id, t.terminal_receipt);
		assert.ok(!fs.existsSync(path.join(env.dir, "results", `${first}.receipt.json`)), "spool removed after publication");
		// Only now does managed dispatch continue.
		b.tick();
		assert.equal(b.sent.length, 2);
		assert.equal(b.sent[1], "second");
		assert.equal(b.sent.filter((s) => s === "first").length, 1, "the settled prompt is never replayed");
	});
}

withEnv("publication retries back off and are surfaced to the human", async (env) => {
	addTask(env.dir, 1, "first");
	const b = await startBridge(env);
	deliverAndAccept(b);
	// Only publication writes the result file, so hits count attempts.
	const f = inject("writeFileSync", /results\/pt_.*\.txt\..*\.tmp$/);
	b.finishTurn("x");
	const after1 = f.hits;
	assert.equal(after1, 1);
	b.tick(); // within backoff: no new attempt
	assert.equal(f.hits, after1, "no attempt inside the backoff window");
	advance(300);
	b.tick();
	assert.equal(f.hits, 2, "retried after the backoff");
	advance(300);
	b.tick();
	assert.equal(f.hits, 2, "backoff grows");
	for (let i = 0; i < 6; i++) {
		advance(60_000);
		b.tick();
	}
	assert.ok(b.ui.notes.some((n) => n.l === "warning" && /could not save the result/.test(n.m)));
	assert.match(b.ui.status, /could not save the result/);
	const hb = readJ(path.join(env.dir, "bridge.json"));
	assert.equal(hb.result_pending, "pt_0000000000000001");
	assert.ok(hb.publish_attempts >= 4);
	clearFaults();
	advance(60_000);
	b.tick();
	assert.ok(b.ui.notes.some((n) => /saved the result/.test(n.m)));
	const fin = events(env.dir).find((e) => e.type === "task_finished");
	assert.ok(fin.data.publish_attempts >= 5);
});

withEnv("human interrupt receipt moves the lease only if control has not changed", async (env) => {
	const id = addTask(env.dir, 1, "first");
	const b = await startBridge(env);
	deliverAndAccept(b);
	lockFault();
	b.finishTurn("partial", "aborted");
	clearFaults();
	// Meanwhile a human took control and released it (new generation).
	await b.commands["devx-takeover"].handler("", b.ctx);
	await b.commands["devx-release"].handler("", b.ctx);
	const gen = agent(env.dir).lease.generation;
	advance(60_000);
	b.tick();
	assert.equal(task(env.dir, id).state, "cancelled");
	assert.equal(agent(env.dir).lease.holder, "managed", "a later release must stand");
	assert.equal(agent(env.dir).lease.generation, gen);
});

withEnv("restart: spooled receipt from a crashed instance is published before dispatch", async (env) => {
	const first = addTask(env.dir, 1, "first");
	const second = addTask(env.dir, 2, "second");
	const a = await startBridge(env);
	deliverAndAccept(a);
	lockFault();
	a.finishTurn("crash answer");
	clearFaults();
	// crash: no shutdown hook. A relaunch (new nonce) takes over.
	const ag = agent(env.dir);
	ag.launch_nonce = "nonce-2";
	real.writeFileSync.call(fs, path.join(env.dir, "agent.json"), JSON.stringify(ag));
	const b = await startBridge(env, { nonce: "nonce-2" });
	assert.equal(task(env.dir, first).state, "completed", "recovered at session_start");
	assert.equal(real.readFileSync.call(fs, path.join(env.dir, "results", `${first}.txt`), "utf8"), "crash answer");
	b.tick();
	assert.deepEqual(b.sent, ["second"]);
	assert.equal(task(env.dir, second).state, "running");
	// The old instance is fenced: it may retry its own receipt (finding it
	// already stamped and logged, so a no-op) but never dispatches.
	a.tick();
	assert.equal(a.sent.length, 1);
	assert.equal(events(env.dir).filter((e) => e.type === "task_finished" && e.task_id === first).length, 1);
});

withEnv("restart: task already marked unknown keeps its state; result is attached once", async (env) => {
	const first = addTask(env.dir, 1, "first");
	const a = await startBridge(env);
	deliverAndAccept(a);
	lockFault();
	a.finishTurn("late answer");
	a.shutdown(); // lock still failing: nothing written, spool kept
	clearFaults();
	assert.ok(fs.existsSync(path.join(env.dir, "results", `${first}.receipt.json`)));
	// devx reconciliation marks the orphaned task unknown.
	const t = task(env.dir, first);
	t.state = "unknown";
	real.writeFileSync.call(fs, path.join(env.dir, "tasks", `${first}.json`), JSON.stringify(t));
	await startBridge(env);
	assert.equal(task(env.dir, first).state, "unknown", "terminal state is never rewritten");
	assert.equal(real.readFileSync.call(fs, path.join(env.dir, "results", `${first}.txt`), "utf8"), "late answer");
	assert.equal(events(env.dir).filter((e) => e.type === "task_result_recovered").length, 1);
	assert.ok(!fs.existsSync(path.join(env.dir, "results", `${first}.receipt.json`)));
});

withEnv("shutdown with an unpublished receipt marks unknown honestly when it can", async (env) => {
	const first = addTask(env.dir, 1, "first");
	const a = await startBridge(env);
	deliverAndAccept(a);
	inject("writeFileSync", /results\/pt_.*\.txt\..*\.tmp$/); // result write keeps failing
	a.finishTurn("answer");
	a.shutdown();
	clearFaults();
	const t = task(env.dir, first);
	assert.equal(t.state, "unknown");
	const ev = events(env.dir).find((e) => e.type === "task_unknown");
	assert.ok(ev.data.receipt_id && ev.data.receipt_spooled === true);
	await startBridge(env); // next instance attaches the preserved result
	assert.equal(real.readFileSync.call(fs, path.join(env.dir, "results", `${first}.txt`), "utf8"), "answer");
	assert.equal(events(env.dir).filter((e) => e.type === "task_result_recovered" && e.data.receipt_id === ev.data.receipt_id).length, 1, "recovery is announced even though task_unknown carries the receipt id");
	assert.ok(!fs.existsSync(path.join(env.dir, "results", `${first}.receipt.json`)));
});

withEnv("corrupt spool is set aside, not fatal", async (env) => {
	addTask(env.dir, 1, "first");
	real.writeFileSync.call(fs, path.join(env.dir, "results", "pt_0000000000000009.receipt.json"), "{not json");
	const b = await startBridge(env);
	b.tick();
	assert.deepEqual(b.sent, ["first"]);
	assert.ok(fs.existsSync(path.join(env.dir, "results", "pt_0000000000000009.receipt.json.corrupt")));
	assert.ok(events(env.dir).some((e) => e.type === "receipt_unreadable"));
});

withEnv("unrecorded delivery claim is not handed to Pi", async (env) => {
	const id = addTask(env.dir, 1, "first");
	const b = await startBridge(env);
	inject("appendFileSync", /events\.jsonl$/, { times: 1 });
	b.tick();
	assert.equal(b.sent.length, 0, "claim not recorded: not handed to Pi");
	b.tick(); // requeue, then a clean claim
	assert.ok(events(env.dir).some((e) => e.type === "delivery_fenced" && e.task_id === id && e.data.reason === "delivery_not_recorded"));
	assert.deepEqual(b.sent, ["first"]);
	assert.equal(events(env.dir).filter((e) => e.type === "task_delivered" && e.task_id === id).length, 1);
});

withEnv("unconfirmed delivery failure is published durably too", async (env) => {
	const id = addTask(env.dir, 1, "first");
	addTask(env.dir, 2, "second");
	const b = await startBridge(env);
	b.tick(); // delivered, never accepted
	advance(20_000);
	inject("writeFileSync", /tasks\/pt_.*\.json\..*\.tmp$/); // tick lock works; publication fails
	b.tick(); // detects the timeout, settles, publication fails
	b.tick();
	assert.equal(task(env.dir, id).state, "running", "not failed until published");
	assert.equal(b.sent.length, 1, "nothing dispatched past the unsaved failure");
	assert.equal(readJ(path.join(env.dir, "bridge.json")).result_pending, id);
	clearFaults();
	advance(60_000);
	b.tick();
	const t = task(env.dir, id);
	assert.equal(t.state, "failed");
	assert.match(t.error, /did not accept/);
	assert.equal(events(env.dir).filter((e) => e.type === "task_finished" && e.task_id === id).length, 1);
	b.tick();
	assert.deepEqual(b.sent, ["first", "second"]);
});

withEnv("human interrupt receipt takes control when control has not changed", async (env) => {
	const id = addTask(env.dir, 1, "first");
	addTask(env.dir, 2, "second");
	const b = await startBridge(env);
	deliverAndAccept(b);
	lockFault();
	b.finishTurn("partial", "aborted");
	clearFaults();
	advance(60_000);
	b.tick();
	const t = task(env.dir, id);
	assert.equal(t.state, "cancelled");
	assert.equal(t.note, "interrupted by a human in Pi");
	assert.equal(agent(env.dir).lease.holder, "human");
	b.tick();
	assert.equal(b.sent.length, 1);
});

withEnv("real lock timeout during publication keeps the result and blocks dispatch", async (env) => {
	const id = addTask(env.dir, 1, "first");
	addTask(env.dir, 2, "second");
	const b = await startBridge(env);
	deliverAndAccept(b);
	const lock = path.join(env.dir, "agent.lock");
	real.mkdirSync.call(fs, lock); // held, fresh by both clocks
	b.finishTurn("answer");
	assert.equal(task(env.dir, id).state, "running");
	assert.ok(fs.existsSync(path.join(env.dir, "results", `${id}.receipt.json`)), "spooled outside the lock");
	fs.rmdirSync(lock);
	advance(60_000);
	b.tick();
	assert.equal(task(env.dir, id).state, "completed");
	b.tick();
	assert.deepEqual(b.sent, ["first", "second"]);
});

withEnv("a spooled receipt whose task record is corrupt is set aside, not fatal", async (env) => {
	addTask(env.dir, 1, "first");
	const bad = "pt_0000000000000009";
	real.writeFileSync.call(fs, path.join(env.dir, "tasks", `${bad}.json`), "{corrupt");
	real.writeFileSync.call(fs, path.join(env.dir, "results", `${bad}.receipt.json`), JSON.stringify({ v: 1, receipt_id: "rc_x", task_id: bad, instance: "old", text: "t", stop: "stop", settled_at: "" }));
	const b = await startBridge(env);
	b.tick();
	assert.deepEqual(b.sent, ["first"]);
	assert.ok(fs.existsSync(path.join(env.dir, "results", `${bad}.receipt.json.corrupt`)));
	assert.ok(events(env.dir).some((e) => e.type === "receipt_unreadable" && e.data.reason === "task_record_unreadable"));
});

withEnv("unreadable task at settle: a requested abort still publishes as 'aborted on request'", async (env) => {
	const id = addTask(env.dir, 1, "first");
	const b = await startBridge(env);
	deliverAndAccept(b);
	const t = task(env.dir, id);
	t.cancel_requested = true;
	real.writeFileSync.call(fs, path.join(env.dir, "tasks", `${id}.json`), JSON.stringify(t));
	inject("readFileSync", new RegExp(`tasks/${id}\\.json$`), { times: 1 });
	inject("writeFileSync", /results\/pt_.*\.txt\..*\.tmp$/, { times: 1 }); // force a deferred publish
	b.finishTurn("partial", "aborted");
	clearFaults();
	advance(60_000);
	b.tick();
	assert.equal(task(env.dir, id).state, "cancelled");
	assert.equal(task(env.dir, id).note, "aborted on request");
	assert.equal(agent(env.dir).lease.holder, "managed");
});

withEnv("unreadable agent at settle: a human interrupt still takes control (delivery generation stands in)", async (env) => {
	const id = addTask(env.dir, 1, "first");
	addTask(env.dir, 2, "second");
	const b = await startBridge(env);
	deliverAndAccept(b);
	inject("readFileSync", /agent\.json$/, { times: 1 });
	b.finishTurn("partial", "aborted");
	clearFaults();
	advance(60_000);
	b.tick();
	b.tick();
	assert.equal(task(env.dir, id).note, "interrupted by a human in Pi");
	assert.equal(agent(env.dir).lease.holder, "human");
	assert.equal(b.sent.length, 1);
});

withEnv("unreadable agent at settle never moves the lease after a later release", async (env) => {
	const id = addTask(env.dir, 1, "first");
	const b = await startBridge(env);
	deliverAndAccept(b);
	inject("readFileSync", /agent\.json$/, { times: 1 });
	lockFault();
	b.finishTurn("partial", "aborted");
	clearFaults();
	await b.commands["devx-takeover"].handler("", b.ctx);
	await b.commands["devx-release"].handler("", b.ctx);
	advance(60_000);
	b.tick();
	assert.equal(task(env.dir, id).state, "cancelled");
	assert.equal(agent(env.dir).lease.holder, "managed");
});

withEnv("stuck recovery is surfaced after repeated failures and blocks dispatch", async (env) => {
	addTask(env.dir, 1, "first");
	const bad = "pt_0000000000000009";
	real.writeFileSync.call(fs, path.join(env.dir, "tasks", `${bad}.json`), JSON.stringify({ id: bad, order: 9, prompt: "x", state: "running", bridge_instance: "old" }));
	real.writeFileSync.call(fs, path.join(env.dir, "results", `${bad}.receipt.json`), JSON.stringify({ v: 1, receipt_id: "rc_y", task_id: bad, instance: "old", text: "t", stop: "stop", settled_at: "" }));
	inject("writeFileSync", /results\/pt_0000000000000009\.txt\..*\.tmp$/);
	const b = await startBridge(env);
	for (let i = 0; i < 5; i++) b.tick();
	assert.equal(b.sent.length, 0, "no dispatch while an earlier result is unpublished");
	assert.ok(b.ui.notes.some((n) => n.l === "warning" && /earlier Pi session/.test(n.m)));
	clearFaults();
	b.tick();
	assert.equal(task(env.dir, bad).state, "completed");
	b.tick();
	assert.deepEqual(b.sent, ["first"]);
});
