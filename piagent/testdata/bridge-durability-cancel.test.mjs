// Durability receipts combined with the protocol-2 cancellation gates
// (abort_sent_at, abortGate, owed aborts). The fault harness in
// bridge-durability.test.mjs registers its own tests and cannot be imported,
// so this file is a minimal copy of that fixture with cancel-specific cases.
//
//   node --experimental-strip-types --test piagent/testdata/bridge-durability-cancel.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { syncBuiltinESMExports } from "node:module";
import { pathToFileURL, fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const BRIDGE = path.join(here, "..", "bridge", "devx-bridge.ts");
const AGENT = "pa_0123456789ab";
const PANE = "%7";
const realMkdir = fs.mkdirSync;
let lockBroken = false;
fs.mkdirSync = function (p, ...rest) {
	if (lockBroken && String(p).endsWith("agent.lock")) throw Object.assign(new Error("injected lock"), { code: "EACCES" });
	return realMkdir.call(fs, p, ...rest);
};
syncBuiltinESMExports();
const realNow = Date.now.bind(Date);
let off = 0;
Date.now = () => realNow() + off;

let seq = 0;
function env() {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "dxbd82-"));
	const dir = path.join(root, "agents", AGENT);
	fs.mkdirSync(path.join(dir, "tasks"), { recursive: true });
	fs.mkdirSync(path.join(dir, "results"), { recursive: true });
	fs.writeFileSync(path.join(dir, "agent.json"), JSON.stringify({ id: AGENT, launch_nonce: "n1", binding: { pane_id: PANE }, lease: { holder: "managed", generation: 1 } }));
	const id = "pt_0000000000000001";
	fs.writeFileSync(path.join(dir, "tasks", `${id}.json`), JSON.stringify({ id, agent_id: AGENT, order: 1, prompt: "work", state: "waiting" }));
	return { root, dir, id };
}
const rj = (f) => JSON.parse(fs.readFileSync(f, "utf8"));
const evs = (dir) => fs.readFileSync(path.join(dir, "events.jsonl"), "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
function patchTask(dir, id, f) {
	const p = path.join(dir, "tasks", `${id}.json`);
	const t = rj(p);
	f(t);
	fs.writeFileSync(p, JSON.stringify(t));
}

async function start(e) {
	Object.assign(process.env, { DEVX_PI_AGENT_ROOT: e.root, DEVX_PI_AGENT_ID: AGENT, DEVX_PI_LAUNCH_NONCE: "n1", TMUX_PANE: PANE });
	const iv = [];
	const o = globalThis.setInterval;
	globalThis.setInterval = (f) => (iv.push(f), {});
	const mod = await import(`${pathToFileURL(BRIDGE).href}?k=${++seq}`);
	const h = {};
	const sent = [];
	const ctx = {
		hasUI: false,
		idle: true,
		aborts: 0,
		ui: {},
		sessionManager: { getSessionId: () => "s", getSessionFile: () => "" },
		isIdle: () => ctx.idle,
		hasPendingMessages: () => false,
		abort: () => ctx.aborts++,
	};
	mod.default({ on: (n, f) => (h[n] = f), registerCommand() {}, sendUserMessage: (t) => sent.push(t) });
	h.session_start({}, ctx);
	globalThis.setInterval = o;
	return { h, ctx, sent, tick: () => iv[0]() };
}

test("requested abort stays 'aborted on request' through a delayed publication, abort sent once", async (t) => {
	const e = env();
	t.after(() => fs.rmSync(e.root, { recursive: true, force: true }));
	const b = await start(e);
	b.tick();
	assert.deepEqual(b.h.input({ text: "work", source: "extension" }, b.ctx), { action: "continue" });
	b.ctx.idle = false;
	patchTask(e.dir, e.id, (x) => ((x.cancel_requested = true), (x.cancel_lease_generation = 1)));
	b.tick();
	assert.equal(b.ctx.aborts, 1);
	lockBroken = true;
	b.h.agent_end({ messages: [{ role: "assistant", stopReason: "aborted", content: [] }] }, b.ctx);
	b.ctx.idle = true;
	b.h.agent_settled({}, b.ctx);
	for (let i = 0; i < 3; i++) b.tick();
	lockBroken = false;
	off += 60_000;
	b.tick();
	b.tick();
	const task = rj(path.join(e.dir, "tasks", `${e.id}.json`));
	assert.equal(task.state, "cancelled");
	assert.equal(task.note, "aborted on request");
	assert.equal(b.ctx.aborts, 1, "at most one abort");
	assert.equal(rj(path.join(e.dir, "agent.json")).lease.holder, "managed");
	assert.equal(evs(e.dir).filter((x) => x.type === "abort_sent").length, 1);
	assert.equal(evs(e.dir).filter((x) => x.type === "task_finished").length, 1);
});

test("a cancel recorded after a human interrupt settled does not relabel it", async (t) => {
	const e = env();
	t.after(() => fs.rmSync(e.root, { recursive: true, force: true }));
	const b = await start(e);
	b.tick();
	b.h.input({ text: "work", source: "extension" }, b.ctx);
	lockBroken = true;
	b.h.agent_end({ messages: [{ role: "assistant", stopReason: "aborted", content: [] }] }, b.ctx);
	b.h.agent_settled({}, b.ctx);
	// devx records a cancel while publication is pending (task still running).
	patchTask(e.dir, e.id, (x) => ((x.cancel_requested = true), (x.cancel_lease_generation = 1)));
	b.ctx.idle = false;
	lockBroken = false;
	b.tick(); // must not abort: the turn already settled
	off += 60_000;
	b.tick();
	const task = rj(path.join(e.dir, "tasks", `${e.id}.json`));
	assert.equal(task.state, "cancelled");
	assert.equal(task.note, "interrupted by a human in Pi");
	assert.equal(b.ctx.aborts, 0);
	assert.ok(!task.abort_sent_at);
	assert.equal(rj(path.join(e.dir, "agent.json")).lease.holder, "human");
});

test("cancel of an unaccepted hand-off is still dropped while a human claim is unsaved", async (t) => {
	const e = env();
	t.after(() => fs.rmSync(e.root, { recursive: true, force: true }));
	const b = await start(e);
	b.tick();
	lockBroken = true;
	assert.deepEqual(b.h.input({ text: "mine", source: "interactive" }, b.ctx), { action: "handled" });
	lockBroken = false;
	patchTask(e.dir, e.id, (x) => (x.cancel_requested = true));
	assert.deepEqual(b.h.input({ text: "work", source: "extension" }, b.ctx), { action: "handled" });
	b.tick();
	b.tick();
	const task = rj(path.join(e.dir, "tasks", `${e.id}.json`));
	assert.equal(task.state, "cancelled", "PR82 pre-acceptance cancel wins over requeue");
	assert.equal(task.note, "cancelled before Pi accepted it");
	assert.equal(rj(path.join(e.dir, "agent.json")).lease.holder, "human");
	assert.equal(b.sent.length, 1);
});

test("an abort the bridge sent that lands mid-tool (no aborted message, se.aborted) is 'aborted on request'", async (t) => {
	const e = env();
	t.after(() => fs.rmSync(e.root, { recursive: true, force: true }));
	const b = await start(e);
	b.tick();
	b.h.input({ text: "work", source: "extension" }, b.ctx);
	b.ctx.idle = false;
	patchTask(e.dir, e.id, (x) => ((x.cancel_requested = true), (x.cancel_lease_generation = 1)));
	b.tick();
	assert.equal(b.ctx.aborts, 1);
	b.ctx.idle = true;
	b.h.agent_end({ messages: [{ role: "assistant", stopReason: "toolUse", content: [] }] }, b.ctx);
	b.h.agent_settled({ aborted: true }, b.ctx);
	const task = rj(path.join(e.dir, "tasks", `${e.id}.json`));
	assert.equal(task.state, "cancelled");
	assert.equal(task.note, "aborted on request");
	assert.ok(task.abort_sent_at);
});

// Owed abort (abort_sent_at save landed, reported failure) and a turn that
// then ends on its own: the frozen receipt says the abort was not
// dispatched, so even a delayed publication withdraws abort_sent_at.
test("an owed abort is withdrawn by a delayed publication when the turn completed on its own", async (t) => {
	const e = env();
	t.after(() => fs.rmSync(e.root, { recursive: true, force: true }));
	const b = await start(e);
	b.tick();
	assert.deepEqual(b.h.input({ text: "work", source: "extension" }, b.ctx), { action: "continue" });
	b.ctx.idle = false;
	patchTask(e.dir, e.id, (x) => ((x.cancel_requested = true), (x.cancel_lease_generation = 1)));
	// Simulate the landed-but-failed save: abort_sent_at is on disk, the abort was not dispatched.
	const realRename = fs.renameSync;
	let once = true;
	fs.renameSync = function (a, c) {
		const r = realRename.call(fs, a, c);
		if (once && String(c).includes("tasks")) {
			once = false;
			throw Object.assign(new Error("injected"), { code: "EIO" });
		}
		return r;
	};
	syncBuiltinESMExports();
	try {
		b.tick();
	} finally {
		fs.renameSync = realRename;
		syncBuiltinESMExports();
	}
	assert.equal(b.ctx.aborts, 0);
	assert.ok(rj(path.join(e.dir, "tasks", `${e.id}.json`)).abort_sent_at, "precondition: owed mark on disk");
	lockBroken = true; // defer publication
	b.ctx.idle = true;
	b.h.agent_end({ messages: [{ role: "assistant", stopReason: "stop", content: [{ type: "text", text: "done" }] }] }, b.ctx);
	b.h.agent_settled({}, b.ctx);
	lockBroken = false;
	off += 60_000;
	b.tick();
	const task = rj(path.join(e.dir, "tasks", `${e.id}.json`));
	assert.equal(task.state, "completed");
	assert.ok(!task.abort_sent_at, "abort_sent_at means ctx.abort() was called; it was not");
	assert.equal(b.ctx.aborts, 0);
});

// A spool from before abort_dispatched existed, for a dispatched abort that
// lost the race to a natural finish: "not dispatched" is not provable, so
// the true abort_sent_at is kept.
test("an old spool without abort_dispatched keeps a dispatched abort's abort_sent_at", async (t) => {
	const e = env();
	t.after(() => fs.rmSync(e.root, { recursive: true, force: true }));
	const sentAt = "2026-10-11T00:00:01.000Z";
	patchTask(e.dir, e.id, (x) => Object.assign(x, { state: "running", delivered_at: sentAt, confirmed_at: sentAt, bridge_instance: "0123456789abcdef", cancel_requested: true, cancel_lease_generation: 1, abort_sent_at: sentAt }));
	fs.writeFileSync(path.join(e.dir, "results", `${e.id}.receipt.json`), JSON.stringify({ v: 2, receipt_id: "rc_00000000000000a1", task_id: e.id, instance: "0123456789abcdef", text: "done", stop: "stop", aborted: false, abort_on_request: false, lease_generation_basis: 1, settled_at: sentAt }));
	const b = await start(e);
	b.tick();
	const task = rj(path.join(e.dir, "tasks", `${e.id}.json`));
	assert.equal(task.state, "completed");
	assert.equal(task.abort_sent_at, sentAt, "kept: the old spool cannot prove the abort was not sent");
});
