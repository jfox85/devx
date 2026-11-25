// Fixture-only tests for the Pi bridge's model handling (protocol 4): a fake
// Pi API and a temporary DevX agent directory (no Pi, tmux or network).
//
//   node --experimental-strip-types --test piagent/testdata/bridge-model.test.mjs
//
// Invariants: the heartbeat reports the current model; a task carrying a
// model switches the session to it BEFORE its prompt reaches Pi, and if the
// switch fails the task fails without the prompt ever being sent; the model
// a task was accepted on is recorded; model changes update agent.json.
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const BRIDGE = process.env.DEVX_BRIDGE_SOURCE || path.join(here, "..", "bridge", "devx-bridge.ts");
const AGENT = "pa_0123456789ab";
const PANE = "%7";

let seq = 0;
function makeEnv(t) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "dxbm-"));
	const dir = path.join(root, "agents", AGENT);
	fs.mkdirSync(path.join(dir, "tasks"), { recursive: true });
	fs.mkdirSync(path.join(dir, "results"), { recursive: true });
	fs.writeFileSync(path.join(dir, "agent.json"), JSON.stringify({ id: AGENT, launch_nonce: "n1", binding: { pane_id: PANE }, lease: { holder: "managed", generation: 1 } }));
	t.after(() => fs.rmSync(root, { recursive: true, force: true }));
	return { root, dir };
}
function addTask(dir, n, prompt, model) {
	const id = `pt_${String(n).padStart(16, "0")}`;
	const tk = { id, agent_id: AGENT, order: n, prompt, state: "waiting" };
	if (model) tk.model = model;
	fs.writeFileSync(path.join(dir, "tasks", `${id}.json`), JSON.stringify(tk));
	return id;
}
const rj = (f) => JSON.parse(fs.readFileSync(f, "utf8"));
const task = (dir, id) => rj(path.join(dir, "tasks", `${id}.json`));
const events = (dir) => {
	try {
		return fs.readFileSync(path.join(dir, "events.jsonl"), "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
	} catch {
		return [];
	}
};
const flush = () => new Promise((r) => setImmediate(r));

const CATALOG = {
	"anthropic/claude-sonnet-4-5": { provider: "anthropic", id: "claude-sonnet-4-5" },
	"openai/gpt-5": { provider: "openai", id: "gpt-5" },
	"nokey/model-x": { provider: "nokey", id: "model-x" },
};

async function startBridge(e, { model = CATALOG["anthropic/claude-sonnet-4-5"], setModel } = {}) {
	Object.assign(process.env, { DEVX_PI_AGENT_ROOT: e.root, DEVX_PI_AGENT_ID: AGENT, DEVX_PI_LAUNCH_NONCE: "n1", TMUX_PANE: PANE });
	delete process.env.DEVX_PI_TEST_DELIVERY_DELAY_MS;
	delete process.env.DEVX_PI_DEVX_EXECUTABLE;
	const iv = [];
	const o = globalThis.setInterval;
	globalThis.setInterval = (f) => (iv.push(f), { unref() {} });
	const h = {};
	const sent = [];
	const switched = [];
	const ctx = {
		hasUI: false,
		idle: true,
		pending: false,
		model,
		ui: {},
		sessionManager: { getSessionId: () => "s", getSessionFile: () => "" },
		modelRegistry: { find: (provider, id) => CATALOG[`${provider}/${id}`] },
		isIdle: () => ctx.idle,
		hasPendingMessages: () => ctx.pending,
		abort() {},
	};
	const api = {
		on: (n, f) => (h[n] = f),
		registerCommand() {},
		sendUserMessage: (t) => sent.push({ text: t, model: ctx.model && `${ctx.model.provider}/${ctx.model.id}` }),
		setModel:
			setModel ??
			(async (m) => {
				if (m.provider === "nokey") return false;
				const previousModel = ctx.model;
				ctx.model = m;
				switched.push(`${m.provider}/${m.id}`);
				h.model_select?.({ model: m, previousModel, source: "set" }, ctx);
				return true;
			}),
	};
	try {
		const mod = await import(`${pathToFileURL(BRIDGE).href}?m=${++seq}`);
		mod.default(api);
		h.session_start({}, ctx);
	} finally {
		globalThis.setInterval = o;
	}
	return {
		h,
		ctx,
		sent,
		switched,
		tick: () => iv[0](),
		accept: () => h.input({ text: sent[sent.length - 1].text, source: "extension" }, ctx),
	};
}

test("the heartbeat reports protocol 4 and the current model", async (t) => {
	const e = makeEnv(t);
	const b = await startBridge(e);
	b.tick();
	const hb = rj(path.join(e.dir, "bridge.json"));
	assert.equal(hb.protocol, 4);
	assert.equal(hb.model, "anthropic/claude-sonnet-4-5");
	b.ctx.model = undefined;
	b.tick();
	assert.equal(rj(path.join(e.dir, "bridge.json")).model, "", "no model selected reports empty");
});

test("a task without a model is delivered as before and records the accepted model", async (t) => {
	const e = makeEnv(t);
	const id = addTask(e.dir, 1, "plain prompt");
	const b = await startBridge(e);
	b.tick();
	assert.equal(b.sent.length, 1, "delivered synchronously");
	assert.equal(b.switched.length, 0, "no switch");
	b.accept();
	const tk = task(e.dir, id);
	assert.equal(tk.accepted_model, "anthropic/claude-sonnet-4-5");
});

test("a task with a model switches before the prompt reaches Pi", async (t) => {
	const e = makeEnv(t);
	const id = addTask(e.dir, 1, "on gpt please", "openai/gpt-5");
	const b = await startBridge(e);
	b.tick();
	await flush();
	assert.deepEqual(b.switched, ["openai/gpt-5"]);
	assert.equal(b.sent.length, 1);
	assert.equal(b.sent[0].model, "openai/gpt-5", "prompt sent only after the switch");
	b.accept();
	const tk = task(e.dir, id);
	assert.equal(tk.accepted_model, "openai/gpt-5");
	const a = rj(path.join(e.dir, "agent.json"));
	assert.equal(a.model, "openai/gpt-5", "agent record follows the switch (relaunch resumes on it)");
	const ev = events(e.dir).find((x) => x.type === "model_changed");
	assert.ok(ev, "model_changed event");
	assert.equal(ev.data.to, "openai/gpt-5");
	b.tick();
	assert.equal(rj(path.join(e.dir, "bridge.json")).model, "openai/gpt-5");
});

test("a task already on the requested model does not switch", async (t) => {
	const e = makeEnv(t);
	addTask(e.dir, 1, "same model", "anthropic/claude-sonnet-4-5");
	const b = await startBridge(e);
	b.tick();
	await flush();
	assert.equal(b.switched.length, 0);
	assert.equal(b.sent.length, 1);
});

for (const [name, model, opts, want] of [
	["unknown to this Pi", "ghost/none", {}, /not available in this Pi/],
	["without credentials", "nokey/model-x", {}, /no credentials configured/],
	["throwing", "openai/gpt-5", { setModel: async () => { throw new Error("boom"); } }, /switching to model openai\/gpt-5 failed: boom/],
]) {
	test(`a failed switch (${name}) fails the task without sending the prompt`, async (t) => {
		const e = makeEnv(t);
		const id = addTask(e.dir, 1, "never run me", model);
		const next = addTask(e.dir, 2, "next one");
		const b = await startBridge(e, opts);
		b.tick();
		await flush();
		assert.equal(b.sent.length, 0, "prompt must not reach Pi");
		const tk = task(e.dir, id);
		assert.equal(tk.state, "failed");
		assert.match(tk.error, want);
		assert.match(tk.error, /the prompt was not run/);
		assert.equal(b.ctx.model.id, "claude-sonnet-4-5", "model unchanged");
		// The queue moves on.
		b.tick();
		assert.equal(b.sent.length, 1);
		assert.equal(b.sent[0].text, "next one");
		assert.equal(task(e.dir, next).state, "running");
	});
}

// A switch that is still in flight when the bridge stops or is fenced must
// never hand the prompt to Pi afterwards.
for (const how of ["shutdown", "fenced", "cancelled"]) {
	test(`a switch in flight when the task is ${how} never sends the prompt`, async (t) => {
		const e = makeEnv(t);
		let release;
		const gate = new Promise((r) => (release = r));
		const id = addTask(e.dir, 1, "slow switch", "openai/gpt-5");
		const b = await startBridge(e, {
			setModel: async (m) => {
				await gate;
				b.ctx.model = m;
				return true;
			},
		});
		b.tick();
		await flush();
		assert.equal(b.sent.length, 0, "waiting on the switch");
		if (how === "shutdown") b.h.session_shutdown({}, b.ctx);
		else if (how === "fenced") {
			const a = rj(path.join(e.dir, "agent.json"));
			a.launch_nonce = "n2";
			fs.writeFileSync(path.join(e.dir, "agent.json"), JSON.stringify(a));
			b.tick();
		} else {
			const tk = task(e.dir, id);
			tk.cancel_requested = true;
			fs.writeFileSync(path.join(e.dir, "tasks", `${id}.json`), JSON.stringify(tk));
		}
		release();
		await flush();
		await flush();
		if (how === "cancelled") {
			// Sent, but the input fence drops it before Pi accepts it.
			assert.deepEqual(b.accept(), { action: "handled" });
			assert.equal(task(e.dir, id).state, "cancelled");
		} else {
			assert.equal(b.sent.length, 0, "a stopped or fenced bridge must not deliver");
		}
	});
}

test("a slow switch does not trip the acceptance timeout", async (t) => {
	const e = makeEnv(t);
	let release;
	const gate = new Promise((r) => (release = r));
	const id = addTask(e.dir, 1, "slow", "openai/gpt-5");
	const b = await startBridge(e, {
		setModel: async (m) => {
			await gate;
			b.ctx.model = m;
			return true;
		},
	});
	const realNow = Date.now;
	t.after(() => (Date.now = realNow));
	b.tick();
	await flush();
	Date.now = () => realNow() + 20000; // past CONFIRM_TIMEOUT_MS
	b.tick();
	assert.equal(task(e.dir, id).state, "running", "not failed while switching");
	release();
	await flush();
	await flush();
	assert.equal(b.sent.length, 1);
	b.tick();
	assert.equal(task(e.dir, id).state, "running", "timer restarted at hand-off");
	b.accept();
	assert.equal(task(e.dir, id).accepted_model, "openai/gpt-5");
});

test("a timed-out switch that is still running fences later deliveries", async (t) => {
	const e = makeEnv(t);
	let release;
	const gate = new Promise((r) => (release = r));
	const id = addTask(e.dir, 1, "slow", "openai/gpt-5");
	const next = addTask(e.dir, 2, "next one");
	const realSetTimeout = globalThis.setTimeout;
	// Make the bridge's 60 s switch timeout fire immediately.
	globalThis.setTimeout = (f, ms, ...a) => realSetTimeout(f, ms >= 60000 ? 0 : ms, ...a);
	t.after(() => (globalThis.setTimeout = realSetTimeout));
	const b = await startBridge(e, {
		setModel: async (m) => {
			await gate;
			b.ctx.model = m;
			return true;
		},
	});
	b.tick();
	await new Promise((r) => realSetTimeout(r, 5));
	await flush();
	assert.equal(task(e.dir, id).state, "failed");
	assert.match(task(e.dir, id).error, /did not finish/);
	b.tick();
	b.tick();
	assert.equal(b.sent.length, 0, "nothing delivered while the switch is outstanding");
	assert.equal(task(e.dir, next).state, "waiting");
	release();
	await flush();
	await flush();
	b.tick();
	assert.equal(b.sent.length, 1);
	assert.equal(b.sent[0].text, "next one");
	assert.equal(b.sent[0].model, "openai/gpt-5", "the late switch landed before, not during, the next task");
});

// Uses a read-only directory to fail the save; Windows ignores directory modes.
test("a rolled-back acceptance drops accepted_model", { skip: process.platform === "win32" }, async (t) => {
	const e = makeEnv(t);
	const id = addTask(e.dir, 1, "x");
	const b = await startBridge(e);
	b.tick();
	// The acceptance save fails (tasks dir read-only): the input handler
	// drops the prompt and schedules requeueLocked.
	const tasks = path.join(e.dir, "tasks");
	fs.chmodSync(tasks, 0o500);
	assert.deepEqual(b.accept(), { action: "handled" });
	fs.chmodSync(tasks, 0o700);
	// ...but suppose it actually landed (it "reported failure"): stale
	// acceptance fields from this instance are on disk when the rollback runs.
	const tk = task(e.dir, id);
	assert.equal(tk.state, "running");
	Object.assign(tk, { confirmed_at: new Date().toISOString(), accepted_as: "turn", accepted_model: "anthropic/claude-sonnet-4-5" });
	fs.writeFileSync(path.join(tasks, `${id}.json`), JSON.stringify(tk));
	b.ctx.idle = false; // keep it from being re-delivered on this tick
	b.tick();
	const after = task(e.dir, id);
	assert.equal(after.state, "waiting");
	assert.equal(after.accepted_model, undefined);
	assert.equal(after.accepted_as, undefined);
	assert.equal(after.confirmed_at, undefined);
});

test("an unsaved first model change on an unpinned agent is retried", async (t) => {
	const e = makeEnv(t);
	const b = await startBridge(e);
	b.tick();
	// The save fails (agent record unreadable for that moment).
	const file = path.join(e.dir, "agent.json");
	const good = fs.readFileSync(file, "utf8");
	fs.writeFileSync(file, "{not json");
	b.ctx.model = CATALOG["openai/gpt-5"];
	b.h.model_select({ model: CATALOG["openai/gpt-5"], source: "set" }, b.ctx);
	fs.writeFileSync(file, good);
	assert.equal(rj(file).model, undefined);
	b.tick();
	assert.equal(rj(file).model, "openai/gpt-5", "retried by the tick");
});

test("an untouched default launch is never pinned", async (t) => {
	const e = makeEnv(t);
	const b = await startBridge(e);
	b.tick();
	b.tick();
	assert.equal(rj(path.join(e.dir, "agent.json")).model, undefined);
});

test("a pinned model that missed its update is reconciled to the live one", async (t) => {
	const e = makeEnv(t);
	const a = rj(path.join(e.dir, "agent.json"));
	a.model = "openai/gpt-5";
	fs.writeFileSync(path.join(e.dir, "agent.json"), JSON.stringify(a));
	const b = await startBridge(e); // live: anthropic/claude-sonnet-4-5
	b.tick();
	assert.equal(rj(path.join(e.dir, "agent.json")).model, "anthropic/claude-sonnet-4-5");
	assert.ok(events(e.dir).some((x) => x.type === "model_changed" && x.data.source === "reconcile"));
});

test("a human /model change updates agent.json; a fenced bridge does not", async (t) => {
	const e = makeEnv(t);
	const b = await startBridge(e);
	b.tick();
	b.h.model_select({ model: CATALOG["openai/gpt-5"], source: "set" }, b.ctx);
	assert.equal(rj(path.join(e.dir, "agent.json")).model, "openai/gpt-5");
	// A newer launch fences this bridge: it must not write the record.
	const a = rj(path.join(e.dir, "agent.json"));
	a.launch_nonce = "n2";
	fs.writeFileSync(path.join(e.dir, "agent.json"), JSON.stringify(a));
	b.tick();
	b.h.model_select({ model: CATALOG["anthropic/claude-sonnet-4-5"], source: "set" }, b.ctx);
	assert.equal(rj(path.join(e.dir, "agent.json")).model, "openai/gpt-5");
});
