// DevX managed-agent bridge for Pi. Generated and loaded by `devx` with
// `pi -e`; inert unless DEVX_PI_AGENT_* environment variables are set.
//
// It runs inside the same interactive Pi TUI the human sees, so a managed
// agent has exactly one live conversation. It:
//   - delivers queued DevX tasks with pi.sendUserMessage (never tmux keys),
//   - fences delivery on the lease: human input or an explicit takeover stops
//     further delivery until a human releases control,
//   - records results and events in the DevX agent directory.
//
// All shared-state changes happen under <agent>/agent.lock (mkdir lock), the
// same lock the devx Go process uses.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { execFile } from "node:child_process";
import { randomBytes } from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";

const ROOT = process.env.DEVX_PI_AGENT_ROOT ?? "";
const AGENT_ID = process.env.DEVX_PI_AGENT_ID ?? "";
const NONCE = process.env.DEVX_PI_LAUNCH_NONCE ?? "";
const DEVX_SESSION = process.env.DEVX_PI_DEVX_SESSION ?? "";
const DEVX_EXE = process.env.DEVX_PI_DEVX_EXECUTABLE ?? "";
const PANE = process.env.TMUX_PANE ?? "";
const TICK_MS = 250;
const CONFIRM_TIMEOUT_MS = 15000;
const LOCK_TIMEOUT_MS = 5000;
const LOCK_STALE_MS = 10000;
// Test hook: delay between claiming a task and handing it to Pi, so tests can
// land a takeover inside that window. Ignored unless set to a positive number.
const TEST_DELIVERY_DELAY_MS = Math.min(10000, parseInt(process.env.DEVX_PI_TEST_DELIVERY_DELAY_MS ?? "0", 10) || 0);

const dir = path.join(ROOT, "agents", AGENT_ID);
const p = {
	agent: path.join(dir, "agent.json"),
	bridge: path.join(dir, "bridge.json"),
	tasks: path.join(dir, "tasks"),
	results: path.join(dir, "results"),
	events: path.join(dir, "events.jsonl"),
	seq: path.join(dir, "seq"),
	lock: path.join(dir, "agent.lock"),
};

function sleepSync(ms: number) {
	Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

function withLock<T>(fn: () => T): T {
	const deadline = Date.now() + LOCK_TIMEOUT_MS;
	for (;;) {
		try {
			fs.mkdirSync(p.lock);
			break;
		} catch (e: any) {
			if (e?.code !== "EEXIST") throw e;
			try {
				if (Date.now() - fs.statSync(p.lock).mtimeMs > LOCK_STALE_MS) {
					fs.rmdirSync(p.lock);
					continue;
				}
			} catch {}
			if (Date.now() > deadline) throw new Error("devx agent lock timeout");
			sleepSync(10);
		}
	}
	try {
		return fn();
	} finally {
		try {
			fs.rmdirSync(p.lock);
		} catch {}
	}
}

function readJSON(file: string): any {
	try {
		return JSON.parse(fs.readFileSync(file, "utf8"));
	} catch {
		return undefined;
	}
}

function writeAtomic(file: string, data: string) {
	const tmp = `${file}.${process.pid}.${randomBytes(4).toString("hex")}.tmp`;
	fs.writeFileSync(tmp, data, { mode: 0o600 });
	fs.renameSync(tmp, file);
}

const writeJSON = (file: string, v: any) => writeAtomic(file, JSON.stringify(v, null, 2));
const now = () => new Date().toISOString();

// Highest seq among complete lines in the last 64 KiB of the event log.
function lastSeqFromLog(): number {
	let max = 0;
	try {
		const size = fs.statSync(p.events).size;
		const start = Math.max(0, size - 65536);
		const fd = fs.openSync(p.events, "r");
		const b = Buffer.alloc(size - start);
		fs.readSync(fd, b, 0, b.length, start);
		fs.closeSync(fd);
		for (const line of b.toString("utf8").split("\n")) {
			try {
				const s = JSON.parse(line)?.seq;
				if (typeof s === "number" && s > max) max = s;
			} catch {}
		}
	} catch {}
	return max;
}

// Caller holds the lock.
function appendEvent(type: string, taskId: string | undefined, data?: Record<string, unknown>) {
	let last = 0;
	try {
		last = parseInt(fs.readFileSync(p.seq, "utf8").trim(), 10) || 0;
	} catch {}
	// seq is a cache; if a writer crashed after appending, the log is ahead.
	last = Math.max(last, lastSeqFromLog());
	const ev: any = { seq: last + 1, time: now(), type, source: "bridge" };
	if (taskId) ev.task_id = taskId;
	if (data) ev.data = data;
	let prefix = "";
	try {
		const size = fs.statSync(p.events).size;
		if (size > 0) {
			const fd = fs.openSync(p.events, "r");
			const b = Buffer.alloc(1);
			fs.readSync(fd, b, 0, 1, size - 1);
			fs.closeSync(fd);
			if (b[0] !== 0x0a) prefix = "\n"; // torn tail from a crashed writer
		}
	} catch {}
	fs.appendFileSync(p.events, prefix + JSON.stringify(ev) + "\n", { mode: 0o600 });
	writeAtomic(p.seq, String(ev.seq));
}

function taskPath(id: string) {
	return path.join(p.tasks, `${id}.json`);
}

function loadTasks(): any[] {
	let names: string[] = [];
	try {
		names = fs.readdirSync(p.tasks);
	} catch {
		return [];
	}
	const out: any[] = [];
	for (const n of names) {
		if (!/^pt_[0-9a-f]{16}\.json$/.test(n)) continue;
		const t = readJSON(path.join(p.tasks, n));
		if (t) out.push(t);
	}
	return out.sort((a, b) => a.order - b.order);
}

function saveTask(t: any) {
	t.updated_at = now();
	writeJSON(taskPath(t.id), t);
}

function setLeaseLocked(holder: "human" | "managed", reason: string, by: string): boolean {
	const a = readJSON(p.agent);
	if (!a || a.lease?.holder === holder) return false;
	const from = a.lease?.holder;
	a.lease = { holder, generation: (a.lease?.generation ?? 0) + 1, since: now(), reason, by };
	a.updated_at = now();
	writeJSON(p.agent, a);
	appendEvent("lease_changed", undefined, { from, to: holder, generation: a.lease.generation, reason });
	return true;
}

function flagAttention(reason: string) {
	if (!DEVX_EXE || !DEVX_SESSION) return;
	execFile(DEVX_EXE, ["session", "flag", DEVX_SESSION, reason], { timeout: 10000 }, () => {});
}

function lastAssistant(messages: any[]): { text: string; stop?: string; error?: string } {
	for (let i = messages.length - 1; i >= 0; i--) {
		const m = messages[i];
		if (m?.role !== "assistant") continue;
		const parts = Array.isArray(m.content) ? m.content : [];
		const text = parts
			.filter((c: any) => c?.type === "text")
			.map((c: any) => c.text)
			.join("");
		return { text, stop: m.stopReason, error: m.errorMessage };
	}
	return { text: "" };
}

export default function devxBridge(pi: ExtensionAPI) {
	if (!ROOT || !/^pa_[0-9a-f]{12}$/.test(AGENT_ID) || !NONCE) return;

	const instance = randomBytes(8).toString("hex");
	let mode: "active" | "fenced" | "stopped" = "active";
	let currentTask: string | undefined; // delivered to this Pi process
	let pendingConfirm: { id: string; prompt: string; at: number } | undefined;
	let lastEnd: { text: string; stop?: string; error?: string } | undefined;
	let abortSent = false;
	let timer: ReturnType<typeof setInterval> | undefined;
	let ctxRef: any;
	let requeueId: string | undefined;

	const statusLine = () => {
		if (!ctxRef?.hasUI) return;
		const a = readJSON(p.agent);
		const holder = a?.lease?.holder;
		const text =
			mode === "fenced"
				? "devx: FENCED (superseded launch) — not accepting tasks"
				: holder === "human"
					? "devx: HUMAN control — /devx-release to resume managed tasks"
					: currentTask
						? `devx: managed · running ${currentTask}`
						: "devx: managed · type to take control";
		ctxRef.ui.setStatus("devx", text);
	};

	const heartbeat = (ctx: any) => {
		let editor = "";
		try {
			editor = ctx.hasUI ? (ctx.ui.getEditorText() ?? "") : "";
		} catch {}
		writeJSON(p.bridge, {
			instance,
			nonce: NONCE,
			pid: process.pid,
			pane: PANE,
			pi_session_id: ctx.sessionManager.getSessionId(),
			pi_session_file: ctx.sessionManager.getSessionFile() ?? "",
			idle: ctx.isIdle(),
			current_task: currentTask ?? "",
			human_typing: editor.trim() !== "",
			heartbeat: now(),
			mode,
		});
		return editor;
	};

	const finishLocked = (t: any, state: string, extra: Record<string, unknown>) => {
		t.state = state;
		t.finished_at = now();
		Object.assign(t, extra);
		saveTask(t);
		appendEvent("task_finished", t.id, { state, stop_reason: t.stop_reason ?? "", result_bytes: t.result_bytes ?? 0, human_intervened: !!t.human_intervened });
	};

	const tick = () => {
		const ctx = ctxRef;
		if (!ctx || mode === "stopped") return;
		let deliver: { id: string; prompt: string } | undefined;
		let doAbort = false;
		try {
			withLock(() => {
				const a = readJSON(p.agent);
				if (!a) return;
				// Fence: only the newest launch, in the bound pane, may act.
				if (a.launch_nonce !== NONCE) mode = "fenced";
				const editor = heartbeat(ctx);
				if (requeueId) {
					const t = readJSON(taskPath(requeueId));
					if (t?.state === "running" && !t.confirmed_at) {
						t.state = "waiting";
						delete t.delivered_at;
						delete t.bridge_instance;
						saveTask(t);
						appendEvent("delivery_fenced", requeueId, { reason: "lock_unavailable" });
					}
					requeueId = undefined;
				}
				if (mode !== "active" || a.binding?.pane_id !== PANE) return;

				if (currentTask) {
					const t = readJSON(taskPath(currentTask));
					if (t?.cancel_requested && !abortSent && !ctx.isIdle()) doAbort = true;
					if (pendingConfirm && Date.now() - pendingConfirm.at > CONFIRM_TIMEOUT_MS + TEST_DELIVERY_DELAY_MS && t?.state === "running") {
						finishLocked(t, "failed", { error: "Pi did not accept the prompt (delivery not confirmed)" });
						currentTask = undefined;
						pendingConfirm = undefined;
					}
					return;
				}
				if (a.lease?.holder !== "managed") return;
				if (!ctx.isIdle() || ctx.hasPendingMessages() || editor.trim() !== "") return;
				const next = loadTasks().find((t) => t.state === "waiting");
				if (!next) return;
				next.state = "running";
				next.delivered_at = now();
				next.bridge_instance = instance;
				next.lease_generation_at_delivery = a.lease.generation;
				saveTask(next);
				appendEvent("task_delivered", next.id, { pane: PANE, lease_generation: a.lease.generation });
				currentTask = next.id;
				abortSent = false;
				lastEnd = undefined;
				pendingConfirm = { id: next.id, prompt: next.prompt, at: Date.now() };
				deliver = { id: next.id, prompt: next.prompt };
			});
		} catch {
			return; // lock contention; retry next tick
		}
		if (doAbort) {
			abortSent = true;
			ctx.abort();
		}
		if (deliver) {
			// followUp avoids a throw if a human submit raced us; the input
			// handler below is the final fence either way.
			const prompt = deliver.prompt;
			if (TEST_DELIVERY_DELAY_MS > 0) setTimeout(() => pi.sendUserMessage(prompt, { deliverAs: "followUp" }), TEST_DELIVERY_DELAY_MS);
			else pi.sendUserMessage(prompt, { deliverAs: "followUp" });
		}
		statusLine();
	};

	pi.on("session_start", (_e, ctx) => {
		ctxRef = ctx;
		try {
			withLock(() => {
				const a = readJSON(p.agent);
				if (!a || a.launch_nonce !== NONCE) mode = "fenced";
				heartbeat(ctx);
				appendEvent("bridge_online", undefined, {
					instance,
					pane: PANE,
					pi_session_id: ctx.sessionManager.getSessionId(),
					expected_pi_session_id: a?.pi_session_id ?? "",
					mode,
					ui: ctx.mode ?? "",
				});
			});
		} catch {}
		timer = setInterval(tick, TICK_MS);
		statusLine();
	});

	pi.on("input", (e) => {
		if (mode !== "active") return { action: "continue" };
		if (e.source === "extension" && pendingConfirm && e.text === pendingConfirm.prompt) {
			const id = pendingConfirm.id;
			let fenced = false;
			try {
			withLock(() => {
				const a = readJSON(p.agent);
				const t = readJSON(taskPath(id));
				if (a?.lease?.holder !== "managed" || t?.state !== "running") {
					// A human took control between selection and delivery:
					// put the task back instead of injecting it.
					fenced = true;
					if (t && t.state === "running") {
						t.state = "waiting";
						delete t.delivered_at;
						delete t.bridge_instance;
						saveTask(t);
					}
					appendEvent("delivery_fenced", id, { reason: a?.lease?.holder === "human" ? "human_control" : `task_${t?.state}` });
				} else {
					t.confirmed_at = now();
					saveTask(t);
					appendEvent("task_started", id, {});
				}
			});
			} catch {
				// Could not confirm under the lock: fail safe. Do not inject;
				// the next tick puts the task back to waiting.
				fenced = true;
				requeueId = id;
			}
			pendingConfirm = undefined;
			if (fenced) {
				currentTask = undefined;
				statusLine();
				return { action: "handled" };
			}
			return { action: "continue" };
		}
		if (e.source === "interactive") {
			try {
				withLock(() => {
					appendEvent("human_input", currentTask, { bytes: e.text.length, during_task: !!currentTask });
					if (currentTask) {
						const t = readJSON(taskPath(currentTask));
						if (t) {
							t.human_intervened = true;
							saveTask(t);
						}
					}
					setLeaseLocked("human", "human_input", "pi-tui");
				});
			} catch {}
			statusLine();
		}
		return { action: "continue" };
	});

	pi.on("agent_end", (e: any) => {
		if (currentTask) lastEnd = lastAssistant(e.messages ?? []);
	});

	pi.on("agent_settled", () => {
		if (!currentTask || pendingConfirm) return;
		const id = currentTask;
		const end = lastEnd ?? { text: "" };
		try {
			withLock(() => {
				const t = readJSON(taskPath(id));
				if (!t || t.state !== "running") return;
				fs.writeFileSync(path.join(p.results, `${id}.txt`), end.text, { mode: 0o600 });
				const extra: Record<string, unknown> = { stop_reason: end.stop ?? "", result_bytes: Buffer.byteLength(end.text) };
				if (end.stop === "aborted") {
					if (t.cancel_requested) {
						finishLocked(t, "cancelled", { ...extra, note: "aborted on request" });
					} else {
						// Interrupted from the TUI: that is a human taking control.
						finishLocked(t, "cancelled", { ...extra, note: "interrupted by a human in Pi", human_intervened: true });
						setLeaseLocked("human", "human_interrupt", "pi-tui");
					}
				} else if (end.stop === "error") {
					finishLocked(t, "failed", { ...extra, error: end.error ?? "Pi reported an error" });
				} else {
					finishLocked(t, "completed", extra);
				}
			});
		} catch {}
		currentTask = undefined;
		lastEnd = undefined;
		flagAttention(`Pi task ${id} finished`);
		statusLine();
	});

	pi.registerCommand("devx-takeover", {
		description: "Take control of this DevX-managed agent (pauses queued remote tasks)",
		handler: async (_args, ctx) => {
			withLock(() => setLeaseLocked("human", "explicit_takeover", "pi-tui"));
			ctx.ui.notify("You have control. Queued DevX tasks are paused until /devx-release.", "info");
			statusLine();
		},
	});

	pi.registerCommand("devx-release", {
		description: "Release control back to DevX managed dispatch",
		handler: async (_args, ctx) => {
			withLock(() => setLeaseLocked("managed", "explicit_release", "pi-tui"));
			ctx.ui.notify("Released. DevX will deliver queued tasks when Pi is idle.", "info");
			statusLine();
		},
	});

	pi.registerCommand("devx-status", {
		description: "Show DevX managed-agent status",
		handler: async (_args, ctx) => {
			const a = readJSON(p.agent);
			const waiting = loadTasks().filter((t) => t.state === "waiting").length;
			ctx.ui.notify(`agent ${AGENT_ID} · control: ${a?.lease?.holder} (gen ${a?.lease?.generation}) · running: ${currentTask ?? "none"} · waiting: ${waiting} · mode: ${mode}`, "info");
		},
	});

	pi.on("session_shutdown", () => {
		if (timer) clearInterval(timer);
		timer = undefined;
		if (mode === "stopped") return;
		const wasActive = mode === "active";
		mode = "stopped";
		try {
			withLock(() => {
				if (wasActive && currentTask) {
					const t = readJSON(taskPath(currentTask));
					if (t?.state === "running") {
						t.state = "unknown";
						t.finished_at = now();
						t.note = "Pi exited while this task was running";
						saveTask(t);
						appendEvent("task_unknown", t.id, { reason: t.note });
					}
				}
				const b = readJSON(p.bridge);
				if (b?.instance === instance) {
					b.mode = "stopped";
					b.heartbeat = now();
					writeJSON(p.bridge, b);
				}
				appendEvent("bridge_offline", undefined, { instance });
			});
		} catch {}
	});
}
