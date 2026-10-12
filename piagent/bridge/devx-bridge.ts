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
//
// Durability rules (both fail closed):
//   - Human ownership: interactive input reaches Pi only after the human
//     lease is durably written. If that fails, the input is rejected and put
//     back in the editor, and an in-memory claim keeps managed dispatch
//     fenced while the bridge retries the lease.
//   - Terminal receipts: a settled turn's outcome is frozen once into a
//     receipt, spooled to results/<id>.receipt.json, and published
//     idempotently (result -> task -> event). The task stays "current", and
//     nothing else is dispatched, until publication succeeds. Retries back
//     off and are surfaced, never replay the prompt, and a later bridge
//     instance recovers spooled receipts before its first dispatch.
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
// Control protocol this bridge implements; must equal piagent.BridgeProtocol.
const PROTOCOL = 2;
const CONFIRM_TIMEOUT_MS = 15000;
const LOCK_TIMEOUT_MS = 5000;
const LOCK_STALE_MS = 10000;
const PUBLISH_BACKOFF_MIN_MS = 250;
const PUBLISH_BACKOFF_MAX_MS = 30000;
const PUBLISH_WARN_AFTER = 4; // failed attempts before the human is warned
const RECEIPT_LOG_SCAN_BYTES = 1 << 20;
// A live Pi is never invisible to devx for longer than this: Go's relaunch
// guard and status rely on the heartbeat.
const SILENT_RECOVERY_MAX_MS = 3000;
// errno codes that can clear on their own; any other per-spool failure is a
// property of that record and quarantines it.
const TRANSIENT_CODES = new Set(["EIO", "EAGAIN", "EBUSY", "EINTR", "EMFILE", "ENFILE", "ENOSPC", "EDQUOT", "ETIMEDOUT"]);
const isTransient = (e: any) => TRANSIENT_CODES.has(e?.code);
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

// Like readJSON, but only a missing file reads as undefined. An unreadable or
// corrupt record throws, so callers that must decide durably never mistake an
// I/O error for "absent".
function readJSONStrict(file: string): any {
	let data: string;
	try {
		data = fs.readFileSync(file, "utf8");
	} catch (e: any) {
		if (e?.code === "ENOENT") return undefined;
		throw e;
	}
	return JSON.parse(data);
}

function writeAtomic(file: string, data: string) {
	const tmp = `${file}.${process.pid}.${randomBytes(4).toString("hex")}.tmp`;
	try {
		fs.writeFileSync(tmp, data, { mode: 0o600 });
		fs.renameSync(tmp, file);
	} catch (e) {
		try {
			fs.rmSync(tmp, { force: true });
		} catch {}
		throw e;
	}
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
	// The log is the source of truth and the event is now in it. seq is only a
	// cache (repaired from the log on the next append), so failing to update it
	// must not make the caller think the event was not recorded.
	try {
		writeAtomic(p.seq, String(ev.seq));
	} catch {}
}

// Whether an event of this type carrying this receipt id is in the tail of
// the event log. Keeps terminal events exactly-once across unknown-outcome
// retries. Other events (e.g. task_unknown) may carry the same receipt id and
// do not count.
function logHasReceipt(receiptId: string, type: string): boolean {
	let size: number;
	try {
		size = fs.statSync(p.events).size;
	} catch (e: any) {
		if (e?.code === "ENOENT") return false;
		throw e;
	}
	const start = Math.max(0, size - RECEIPT_LOG_SCAN_BYTES);
	const fd = fs.openSync(p.events, "r");
	try {
		const b = Buffer.alloc(size - start);
		fs.readSync(fd, b, 0, b.length, start);
		for (const line of b.toString("utf8").split("\n")) {
			if (!line.includes(receiptId)) continue;
			try {
				const ev = JSON.parse(line);
				if (ev?.type === type && ev?.data?.receipt_id === receiptId) return true;
			} catch {}
		}
	} finally {
		fs.closeSync(fd);
	}
	return false;
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

// Caller holds the lock. Every write bumps the revision (the Go side does
// the same), so an edit of pending work can never overwrite a claim.
function saveTask(t: any) {
	t.updated_at = now();
	t.revision = (typeof t.revision === "number" ? t.revision : 0) + 1;
	writeJSON(taskPath(t.id), t);
}

// Caller holds the lock. Returns false when the holder is already `holder`.
// Throws unless the lease is durably `holder` afterwards: a missing or
// unreadable agent record is a failure, not a no-op. The lease_changed event
// is recorded best effort; agent.json is the authority.
function setLeaseLocked(holder: "human" | "managed", reason: string, by: string): boolean {
	const a = readJSONStrict(p.agent);
	if (!a) throw new Error("devx agent record missing");
	if (a.lease?.holder === holder) return false;
	const from = a.lease?.holder;
	a.lease = { holder, generation: (a.lease?.generation ?? 0) + 1, since: now(), reason, by };
	a.updated_at = now();
	writeJSON(p.agent, a);
	try {
		appendEvent("lease_changed", undefined, { from, to: holder, generation: a.lease.generation, reason });
	} catch {}
	return true;
}

// A settled turn's outcome: a closed record, decided once at settle time from
// bridge-owned state and published idempotently. Publication (possibly later,
// possibly by another instance) reads durable task state only for identity
// and terminality, never to decide the outcome.
type Receipt = {
	v: 2;
	receipt_id: string;
	task_id: string;
	instance: string;
	text: string;
	stop: string;
	error?: string;
	// The turn ended in an abort.
	aborted: boolean;
	// That abort was the one this bridge dispatched for a DevX cancel.
	abort_on_request: boolean;
	// This bridge called ctx.abort() for a DevX cancel of this turn, however
	// the turn ended (#82). Publication withdraws an abort_sent_at it did not
	// dispatch; a spool without this field withdraws only when that is
	// provable (aborted and not on request).
	abort_dispatched?: boolean;
	// Lease generation read at settle; null if that read failed. A human
	// interrupt moves the lease only if control has not changed since the
	// turn settled (same generation, or a lease older than settled_at).
	lease_generation_basis: number | null;
	// Set when the bridge itself fails the task (Pi never accepted the
	// prompt): no result text, state failed with this error.
	fail_error?: string;
	settled_at: string;
};

const RECEIPT_ID_RE = /^rc_[0-9a-f]{16}$/;
const TASK_ID_RE = /^pt_[0-9a-f]{16}$/;

// Full structural check of a spooled receipt; returns why it is invalid.
function receiptProblem(r: any, file: string): string {
	if (!r || typeof r !== "object" || Array.isArray(r)) return "not_an_object";
	if (r.v !== 2) return "unsupported_version";
	if (typeof r.receipt_id !== "string" || !RECEIPT_ID_RE.test(r.receipt_id)) return "bad_receipt_id";
	if (typeof r.task_id !== "string" || !TASK_ID_RE.test(r.task_id) || file !== `${r.task_id}.receipt.json`) return "bad_task_id";
	if (typeof r.instance !== "string" || r.instance === "") return "bad_instance";
	if (typeof r.text !== "string") return "bad_text";
	if (typeof r.stop !== "string") return "bad_stop";
	if (r.error !== undefined && typeof r.error !== "string") return "bad_error";
	if (typeof r.aborted !== "boolean" || typeof r.abort_on_request !== "boolean") return "bad_abort";
	if (r.abort_on_request && !r.aborted) return "bad_abort";
	if (r.abort_dispatched !== undefined && typeof r.abort_dispatched !== "boolean") return "bad_abort";
	if (r.abort_on_request && r.abort_dispatched === false) return "bad_abort";
	if (!(r.lease_generation_basis === null || Number.isInteger(r.lease_generation_basis))) return "bad_generation";
	if (r.fail_error !== undefined && typeof r.fail_error !== "string") return "bad_fail_error";
	if (typeof r.settled_at !== "string") return "bad_settled_at";
	return "";
}

const receiptPath = (taskId: string) => path.join(p.results, `${taskId}.receipt.json`);

// Caller holds the lock. Publishes a receipt so that, whatever earlier
// attempts managed before failing, the end state is: result file written,
// task terminal and stamped with the receipt, exactly one terminal event.
// Never overwrites a task someone else already made terminal.
function publishReceiptLocked(r: Receipt, attempts: number): "published" | "superseded" | "gone" {
	const t = readJSONStrict(taskPath(r.task_id));
	if (t === undefined) return "gone"; // the task record was removed (agent records deleted)
	if (!t || typeof t !== "object" || Array.isArray(t) || t.id !== r.task_id) throw new TypeError("task record is not a valid task");
	const resultFile = path.join(p.results, `${r.task_id}.txt`);
	if (t.terminal_receipt === r.receipt_id) {
		// Task already published by an attempt whose outcome we did not see.
		if (!logHasReceipt(r.receipt_id, "task_finished")) {
			appendEvent("task_finished", t.id, { state: t.state, stop_reason: t.stop_reason ?? "", result_bytes: t.result_bytes ?? 0, human_intervened: !!t.human_intervened, receipt_id: r.receipt_id, publish_attempts: attempts });
		}
		return "published";
	}
	if (t.state !== "running" || (t.bridge_instance && t.bridge_instance !== r.instance)) {
		// Someone else already made this task terminal (e.g. devx marked it
		// unknown after this Pi went quiet). Terminal states never change, so
		// do not rewrite it; attach the preserved outcome instead.
		if (!r.fail_error && !fs.existsSync(resultFile)) writeAtomic(resultFile, r.text);
		if (!logHasReceipt(r.receipt_id, "task_result_recovered")) {
			appendEvent("task_result_recovered", t.id, { receipt_id: r.receipt_id, task_state: t.state, stop_reason: r.fail_error ? "" : r.stop, result_bytes: Buffer.byteLength(r.text), ...(r.fail_error ? { error: r.fail_error } : {}) });
		}
		return "superseded";
	}
	// abort_sent_at means this bridge called ctx.abort(). One left by a save
	// that landed but reported failure, whose abort was never dispatched
	// before the turn ended (however it ended), is withdrawn (#82). The task
	// is still bound to r.instance (checked above).
	// Spools without abort_dispatched (written before it existed) prove
	// "not dispatched" only for an abort that was not on request; otherwise
	// unknowable, so the mark is kept.
	if (t.abort_sent_at && !(r.abort_dispatched ?? (r.abort_on_request || !r.aborted))) delete t.abort_sent_at;
	let extra: Record<string, unknown> = { stop_reason: r.stop, result_bytes: Buffer.byteLength(r.text) };
	if (r.fail_error) extra = { error: r.fail_error };
	else writeAtomic(resultFile, r.text);
	let state = "completed";
	if (r.fail_error) {
		state = "failed";
	} else if (r.aborted) {
		state = "cancelled";
		if (r.abort_on_request) {
			extra.note = "aborted on request";
		} else {
			// Interrupted from the TUI: that is a human taking control. Only
			// if control has not changed since the turn settled; a human may
			// have released meanwhile and that must stand.
			extra.note = "interrupted by a human in Pi";
			extra.human_intervened = true;
			const a = readJSONStrict(p.agent);
			if (a && a.lease?.holder === "managed" && controlUnchangedSince(a.lease, r)) {
				setLeaseLocked("human", "human_interrupt", "pi-tui");
			}
		}
	} else if (r.stop === "error") {
		state = "failed";
		extra.error = r.error ?? "Pi reported an error";
	}
	t.state = state;
	t.finished_at = now();
	t.terminal_receipt = r.receipt_id;
	Object.assign(t, extra);
	saveTask(t);
	appendEvent("task_finished", t.id, { state, stop_reason: t.stop_reason ?? "", result_bytes: t.result_bytes ?? 0, human_intervened: !!t.human_intervened, receipt_id: r.receipt_id, publish_attempts: attempts });
	return "published";
}

// Caller holds the lock. Sets a spool aside (kept for inspection, never
// deleted) and records why.
const quarantineLogged = new Set<string>();
function quarantineSpool(name: string, reason: string, receiptId?: string) {
	const file = path.join(p.results, name);
	// Unique, so an earlier quarantined copy is never overwritten. The event
	// goes first: if the rename then fails, recovery retries and records it
	// again rather than losing the record of why.
	// The event is written once per spool file (a retried quarantine whose
	// rename failed must not flood the log).
	const aside = `${name}.${Date.now()}.${randomBytes(3).toString("hex")}.corrupt`;
	if (!quarantineLogged.has(name)) {
		appendEvent("receipt_unreadable", TASK_ID_RE.test(name.slice(0, 19)) ? name.slice(0, 19) : undefined, { file: aside, reason, ...(receiptId ? { receipt_id: receiptId } : {}) });
		quarantineLogged.add(name);
	}
	fs.renameSync(file, path.join(p.results, aside));
}

// Whether the managed lease has not changed since the receipt's turn
// settled: the generation read at settle matches, or (if that read failed)
// the lease was granted before the settle. Both times come from this host.
function controlUnchangedSince(lease: any, r: Receipt): boolean {
	if (r.lease_generation_basis !== null) return lease.generation === r.lease_generation_basis;
	const since = Date.parse(lease.since ?? "");
	const settled = Date.parse(r.settled_at);
	// Strictly earlier: a change in the same millisecond is ambiguous, and
	// ambiguity never moves the lease.
	return Number.isFinite(since) && Number.isFinite(settled) && since < settled;
}

// Spooled receipts left by earlier bridge instances (Pi exited before their
// outcome was published). Invalid spools are quarantined.
// deferred: spool file names this instance skips (permanent errno); left on
// disk for a later instance.
function foreignReceipts(instance: string, deferred?: Set<string>, onDefer?: (name: string, reason: string) => void): Receipt[] {
	let names: string[];
	try {
		names = fs.readdirSync(p.results);
	} catch (e: any) {
		if (e?.code === "ENOENT") return [];
		throw e;
	}
	const out: Receipt[] = [];
	for (const n of names) {
		if (!/^pt_[0-9a-f]{16}\.receipt\.json$/.test(n)) continue;
		if (deferred?.has(n)) continue;
		const file = path.join(p.results, n);
		let r: any;
		let problem = "";
		try {
			r = readJSONStrict(file);
			problem = receiptProblem(r, n);
		} catch (e: any) {
			// Same rule as publication: only errors that can clear are
			// retried; anything else is a property of this file.
			if (isTransient(e)) throw e;
			if (e?.code && deferred && onDefer) {
				// An errno reading the file (e.g. a permission change) is
				// not a property of the receipt: keep it for a later instance.
				deferred.add(n);
				onDefer(n, `spool_unreadable ${e.code}`);
				continue;
			}
			problem = e instanceof SyntaxError ? "unparseable" : `spool_unreadable${e?.code ? ` ${e.code}` : ""}`;
		}
		// Spools are written atomically, so an invalid one is not a torn
		// write that might complete: set it aside instead of blocking.
		if (problem) quarantineSpool(n, problem, typeof r?.receipt_id === "string" ? r.receipt_id : undefined);
		else if (r.instance !== instance) out.push(r);
	}
	return out;
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
	let abortSent = false; // this bridge called ctx.abort() for a DevX cancel of currentTask
	// The task this bridge tried to mark abort_sent_at for. If the save landed
	// but reported failure, the abort is owed: dispatch it (gates permitting)
	// or truthfully withdraw the mark. Never leave "sent" without sending.
	let abortAttempt: string | undefined;
	let timer: ReturnType<typeof setInterval> | undefined;
	let ctxRef: any;
	let requeueId: string | undefined;
	let requeueReason = "lock_unavailable";
	// Human input arrived but the human lease could not be saved. Until it is,
	// the tick retries it and never dispatches managed work.
	let humanClaimPending: string | undefined;
	// Settled turn whose outcome is not yet durably published. currentTask
	// stays set (so nothing else is dispatched) until it is.
	let receipt: Receipt | undefined;
	let receiptSpooled = false;
	let publishAttempts = 0;
	let publishNextAt = 0;
	let publishError = "";
	let publishWarned = false;
	// Receipts spooled by earlier bridge instances must be published before
	// this instance dispatches anything.
	let recovered = false;
	let announced = false; // bridge_online recorded for this instance
	let startedAt = Date.now();
	// A valid predecessor spool whose task is still running under another
	// instance is pending: devx's orphan rule would turn it unknown if it saw
	// this instance live. Only then, and only briefly, is liveness withheld.
	let orphanablePending = false;

	const statusLine = () => {
		if (!ctxRef?.hasUI) return;
		const a = readJSON(p.agent);
		const holder = a?.lease?.holder;
		const text =
			mode === "fenced"
				? "devx: FENCED (superseded launch) — not accepting tasks"
				: receipt && publishWarned
					? `devx: could not save the result of ${receipt.task_id} (retrying; ${publishError})`
					: humanClaimPending
						? "devx: taking control… (could not save yet; retrying)"
						: mode === "active" && !recovered
							? "devx: recovering an earlier Pi session's result — queued tasks wait"
						: holder === "human"
							? "devx: HUMAN control — /devx-release to resume managed tasks"
							: currentTask
								? `devx: managed · running ${currentTask}`
								: "devx: managed · type to take control";
		ctxRef.ui.setStatus("devx", text);
	};

	const notify = (msg: string, level: "info" | "warning" | "error") => {
		try {
			if (ctxRef?.hasUI) ctxRef.ui.notify(msg, level);
		} catch {}
	};

	const spoolReceipt = () => {
		if (!receipt || receiptSpooled) return;
		writeAtomic(receiptPath(receipt.task_id), JSON.stringify(receipt));
		receiptSpooled = true;
	};

	// Freeze the outcome of the current task and try to publish it now. The
	// task stays current until publication succeeds.
	const settle = (r: { text: string; stop: string; error?: string; aborted?: boolean; abort_on_request?: boolean; abort_dispatched?: boolean; fail_error?: string }) => {
		if (!currentTask || receipt) return;
		// Generation basis for judging a human interrupt (see
		// controlUnchangedSince): read now; null if that fails.
		let basis: number | null = null;
		try {
			const g = readJSONStrict(p.agent)?.lease?.generation;
			if (Number.isInteger(g)) basis = g;
		} catch {}
		const out: Receipt = {
			v: 2,
			receipt_id: `rc_${randomBytes(8).toString("hex")}`,
			task_id: currentTask,
			instance,
			text: r.text,
			stop: r.stop,
			aborted: !!r.aborted,
			abort_on_request: !!r.aborted && !!r.abort_on_request,
			abort_dispatched: !!r.abort_dispatched,
			lease_generation_basis: basis,
			settled_at: now(),
		};
		if (r.error !== undefined) out.error = r.error;
		if (r.fail_error !== undefined) out.fail_error = r.fail_error;
		receipt = out;
		receiptSpooled = false;
		publishAttempts = 0;
		publishNextAt = 0;
		publishError = "";
		publishWarned = false;
		// Spool first, outside the agent lock: the result survives a Pi exit
		// even while the lock is unavailable.
		try {
			spoolReceipt();
		} catch {}
		tryPublish();
	};

	// One bounded publication attempt (respecting backoff). Returns true when
	// no receipt is pending afterwards.
	const tryPublish = (force = false): boolean => {
		if (!receipt) return true;
		if (!force && Date.now() < publishNextAt) return false;
		const r = receipt;
		publishAttempts++;
		let outcome: "published" | "superseded" | "gone";
		try {
			outcome = withLock(() => {
				try {
					spoolReceipt();
				} catch {}
				const res = publishReceiptLocked(r, publishAttempts);
				try {
					fs.rmSync(receiptPath(r.task_id), { force: true });
				} catch {}
				return res;
			});
		} catch (e: any) {
			publishError = String(e?.message ?? e).slice(0, 200);
			publishNextAt = Date.now() + Math.min(PUBLISH_BACKOFF_MAX_MS, PUBLISH_BACKOFF_MIN_MS * 2 ** Math.min(publishAttempts - 1, 16));
			if (publishAttempts >= PUBLISH_WARN_AFTER && !publishWarned) {
				publishWarned = true;
				notify(`DevX could not save the result of task ${r.task_id} (${publishError}). It is kept and retried; queued tasks wait until it is saved.`, "warning");
				flagAttention(`Pi task ${r.task_id} result not saved yet`);
			}
			statusLine();
			return false;
		}
		const warned = publishWarned;
		receipt = undefined;
		receiptSpooled = false;
		currentTask = undefined;
		lastEnd = undefined;
		publishWarned = false;
		if (warned) notify(`DevX saved the result of task ${r.task_id} after ${publishAttempts} attempts.`, "info");
		if (outcome !== "gone") flagAttention(`Pi task ${r.task_id} finished`);
		statusLine();
		return true;
	};

	// Caller holds the lock. Publishes receipts left by earlier instances;
	// throws if any could not be published so dispatch stays blocked.
	let recoveryFailures = 0;
	// Valid spools whose publication failed with a permanent errno in this
	// instance: skipped here (so dispatch is not blocked forever) but left on
	// disk, so a later instance retries once the fault (for example a
	// permission change) is fixed. Only structurally invalid spools, which can
	// never publish, are quarantined.
	const deferredSpools = new Set<string>();
	const recoverLocked = () => {
		try {
			// Agent-wide failures are retried and never charged to a spool.
			if (!readJSONStrict(p.agent)) throw new Error("devx agent record missing");
			// Every spool ends published, attached, quarantined (invalid),
			// deferred to a later instance (permanent errno), or retried.
			let pending = false;
			let firstError: any;
			const onDefer = (name: string, reason: string) => {
				appendEvent("receipt_deferred", TASK_ID_RE.test(name.slice(0, 19)) ? name.slice(0, 19) : undefined, { file: name, reason });
				notify(`DevX could not read a result saved by an earlier Pi session (${reason}). It is kept on disk and retried after the next relaunch.`, "warning");
				flagAttention("Pi agent result kept, not yet published");
			};
			for (const r of foreignReceipts(instance, deferredSpools, onDefer)) {
				try {
					publishReceiptLocked(r, 0);
				} catch (e: any) {
					if (isTransient(e)) {
						pending = true;
						firstError ??= e;
						try {
							const t = readJSONStrict(taskPath(r.task_id));
							if (t?.state === "running" && t.bridge_instance !== instance) orphanablePending = true;
						} catch {
							orphanablePending = true; // unknown: assume it could be
						}
						continue;
					}
					const reason = e instanceof SyntaxError ? "task_record_unreadable" : `publish_error${e?.code ? ` ${e.code}` : ""}: ${String(e?.message ?? e).slice(0, 120)}`;
					if (e?.code) {
						// An errno is a property of the environment, not of this
						// receipt: keep the spool for a later instance.
						deferredSpools.add(`${r.task_id}.receipt.json`);
						appendEvent("receipt_deferred", r.task_id, { receipt_id: r.receipt_id, reason });
						notify(`DevX could not publish a result saved by an earlier Pi session (${reason}). It is kept on disk and retried after the next relaunch.`, "warning");
						flagAttention("Pi agent result kept, not yet published");
						continue;
					}
					quarantineSpool(`${r.task_id}.receipt.json`, reason, r.receipt_id);
					continue;
				}
				try {
					fs.rmSync(receiptPath(r.task_id), { force: true });
				} catch {}
			}
			if (pending) throw firstError;
			orphanablePending = false;
		} catch (e: any) {
			if (++recoveryFailures === PUBLISH_WARN_AFTER) {
				notify(`DevX could not publish a result saved by an earlier Pi session (${String(e?.message ?? e).slice(0, 120)}). Queued tasks wait until it is saved.`, "warning");
				flagAttention("Pi agent result recovery pending");
			}
			throw e;
		}
		recovered = true;
		recoveryFailures = 0;
	};

	const liveAllowed = () => mode !== "active" || recovered || !orphanablePending || Date.now() - startedAt >= SILENT_RECOVERY_MAX_MS;

	const announceLocked = (ctx: any, a: any) => {
		appendEvent("bridge_online", undefined, {
			instance,
			pane: PANE,
			pi_session_id: ctx.sessionManager.getSessionId(),
			expected_pi_session_id: a?.pi_session_id ?? "",
			mode,
			ui: ctx.mode ?? "",
		});
		announced = true;
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
			protocol: PROTOCOL,
			...(receipt ? { result_pending: receipt.task_id, publish_attempts: publishAttempts, publish_error: publishError } : {}),
			...(humanClaimPending ? { human_claim_pending: true } : {}),
		});
		return editor;
	};

	// Events annotate durable state; they never gate acting on it. Caller
	// holds the lock.
	const annotate = (type: string, taskId: string | undefined, data?: Record<string, unknown>) => {
		try {
			appendEvent(type, taskId, data);
		} catch {}
	};

	// Why an abort for task t must not be sent now ("" = may send).
	const abortGate = (t: any, a: any, ctx: any): string => {
		if (a.lease?.holder !== "managed") return "human_control";
		if ((t.cancel_lease_generation ?? a.lease.generation) !== a.lease.generation) return "control_changed_since_request";
		if (ctx.hasPendingMessages()) return "pi_has_queued_messages";
		return "";
	};

	// Caller holds the lock. This bridge fenced requeueId: Pi never ran it, so
	// it goes back to waiting (or is cancelled if a cancel arrived). A
	// confirmed_at or human_intervened here is ours from a write that landed
	// but reported failure, or from input before acceptance; neither is true
	// of a turn that never ran, so both are withdrawn.
	const requeueLocked = () => {
		if (!requeueId) return;
		const t = readJSON(taskPath(requeueId));
		const ours = t?.state === "running" && (!t.confirmed_at || t.bridge_instance === instance);
		if (ours) {
			delete t.confirmed_at;
			delete t.accepted_as;
			delete t.human_intervened;
		}
		if (ours && t.cancel_requested) {
			t.state = "cancelled";
			t.finished_at = now();
			t.note = "cancelled before Pi accepted it";
			saveTask(t);
			annotate("task_cancelled", requeueId, { delivered: true, accepted: false });
		} else if (ours) {
			t.state = "waiting";
			delete t.delivered_at;
			delete t.bridge_instance;
			saveTask(t);
			annotate("delivery_fenced", requeueId, { reason: requeueReason });
		}
		requeueId = undefined;
		requeueReason = "lock_unavailable";
	};

	const tick = () => {
		const ctx = ctxRef;
		if (!ctx || mode === "stopped") return;
		// A pending terminal receipt is published first (with backoff); it
		// keeps currentTask set, so nothing below can dispatch past it.
		if (receipt) tryPublish();
		let deliver: { id: string; prompt: string } | undefined;
		let doAbort = false;
		let failUnconfirmed = false;
		let lockFailed = false;
		try {
			withLock(() => {
				const a = readJSON(p.agent);
				if (!a) return;
				// Fence: only the newest launch, in the bound pane, may act.
				if (a.launch_nonce !== NONCE) mode = "fenced";
				// Reconcile predecessors' spools before any dispatch. Liveness is
				// withheld only while that would let devx orphan a spooled
				// running task, and never beyond SILENT_RECOVERY_MAX_MS: a
				// live Pi must not look exited (devx would then relaunch it).
				if (mode === "active" && !recovered) {
					try {
						recoverLocked();
					} catch {}
				}
				if (!liveAllowed()) return;
				const editor = heartbeat(ctx);
				if (!announced) announceLocked(ctx, a);
				requeueLocked();
				if (mode !== "active" || a.binding?.pane_id !== PANE) return;
				if (humanClaimPending) {
					// Unsaved human ownership: save it before anything else.
					// If this throws, the tick dispatches nothing.
					setLeaseLocked("human", humanClaimPending, "pi-tui");
					humanClaimPending = undefined;
					return; // decide dispatch next tick, from the saved lease
				}
				if (!recovered) return; // dispatch stays fenced until reconciled

				if (currentTask) {
					if (receipt) return; // settled; only publication remains
					const t = readJSON(taskPath(currentTask));
					// Abort only a turn Pi accepted, at most once, and only while
					// DevX still holds control at the generation the cancel was
					// requested under. Never under human control: the human owns
					// running work. Never while Pi holds queued messages: in the
					// TUI ctx.abort() moves them into the editor, mixing them with
					// a human's draft. Every decision is recorded.
					if (t?.state === "running" && t.abort_sent_at && !abortSent && abortAttempt === t.id && !pendingConfirm) {
						// Owed: our abort_sent_at save landed but reported
						// failure, so ctx.abort() was not called. The gates are
						// re-checked now: control may have moved meanwhile.
						const withheld = abortGate(t, a, ctx);
						if (withheld || ctx.isIdle()) {
							delete t.abort_sent_at;
							if (withheld) t.abort_withheld = withheld;
							saveTask(t);
							abortAttempt = undefined;
							if (withheld) annotate("abort_withheld", t.id, { reason: withheld, lease_generation: a.lease?.generation ?? 0, abort_not_dispatched: true });
						} else {
							doAbort = true;
						}
					} else if (t?.state === "running" && t.cancel_requested && t.confirmed_at && !t.abort_sent_at && !t.abort_withheld && !abortSent && !pendingConfirm) {
						const withheld = abortGate(t, a, ctx);
						if (withheld) {
							t.abort_withheld = withheld;
							saveTask(t);
							annotate("abort_withheld", t.id, { reason: withheld, lease_generation: a.lease?.generation ?? 0 });
						} else if (!ctx.isIdle()) {
							abortAttempt = t.id;
							t.abort_sent_at = now();
							saveTask(t);
							// Durable from here: the abort is dispatched even if
							// the annotation below cannot be written.
							doAbort = true;
							annotate("abort_sent", t.id, { lease_generation: a.lease.generation });
						}
					}
					if (pendingConfirm && Date.now() - pendingConfirm.at > CONFIRM_TIMEOUT_MS + TEST_DELIVERY_DELAY_MS && t?.state === "running") {
						pendingConfirm = undefined;
						// Published after the lock is released (below).
						failUnconfirmed = true;
					}
					return;
				}
				if (a.lease?.holder !== "managed") return;
				if (!ctx.isIdle() || ctx.hasPendingMessages() || editor.trim() !== "") return;
				// Never pick a waiting task that carries a cancel request.
				const next = loadTasks().find((t) => t.state === "waiting" && !t.cancel_requested);
				if (!next) return;
				next.state = "running";
				next.delivered_at = now();
				next.bridge_instance = instance;
				next.lease_generation_at_delivery = a.lease.generation;
				saveTask(next);
				try {
					appendEvent("task_delivered", next.id, { pane: PANE, lease_generation: a.lease.generation });
				} catch {
					// The task is claimed but the claim is not fully recorded.
					// Do not hand it to Pi; put it back on a later tick.
					requeueId = next.id;
					requeueReason = "delivery_not_recorded";
					return;
				}
				currentTask = next.id;
				abortSent = false;
				lastEnd = undefined;
				pendingConfirm = { id: next.id, prompt: next.prompt, at: Date.now() };
				deliver = { id: next.id, prompt: next.prompt };
			});
		} catch {
			lockFailed = true; // lock contention or a write failure; retry next tick
		}
		if (failUnconfirmed) settle({ text: "", stop: "", fail_error: "Pi did not accept the prompt (delivery not confirmed)" });
		if (doAbort) {
			abortSent = true;
			ctx.abort();
		}
		if (lockFailed) return;
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
				// Publish outcomes an earlier instance settled but could not
				// save before devx sees this instance online (see liveAllowed).
				startedAt = Date.now();
				if (mode === "active") {
					try {
						recoverLocked();
					} catch {}
				}
				if (!liveAllowed()) return;
				heartbeat(ctx);
				announceLocked(ctx, a);
			});
		} catch {}
		timer = setInterval(tick, TICK_MS);
		statusLine();
	});

	pi.on("input", (e, ctx) => {
		if (mode !== "active") return { action: "continue" };
		if (e.source === "extension" && pendingConfirm && e.text === pendingConfirm.prompt && humanClaimPending) {
			// A human submitted after this task was picked and that takeover
			// is not saved yet: the human owns Pi. Do not inject.
			requeueId = pendingConfirm.id;
			requeueReason = "human_control";
			pendingConfirm = undefined;
			currentTask = undefined;
			statusLine();
			return { action: "handled" };
		}
		if (e.source === "extension" && pendingConfirm && e.text === pendingConfirm.prompt) {
			const id = pendingConfirm.id;
			let fenced = false;
			try {
			withLock(() => {
				const a = readJSON(p.agent);
				const t = readJSON(taskPath(id));
				if (t?.state === "running" && t.cancel_requested) {
					// Cancelled after the hand-off but before Pi accepted it:
					// drop the injection; Pi never starts this turn.
					fenced = true;
					t.state = "cancelled";
					t.finished_at = now();
					t.note = "cancelled before Pi accepted it";
					saveTask(t);
					appendEvent("task_cancelled", id, { delivered: true, accepted: false });
				} else if (a?.lease?.holder !== "managed" || t?.state !== "running") {
					// A human took control between selection and delivery:
					// put the task back instead of injecting it.
					fenced = true;
					if (t && t.state === "running") {
						t.state = "waiting";
						delete t.delivered_at;
						delete t.bridge_instance;
						delete t.human_intervened; // it never ran
						saveTask(t);
					}
					appendEvent("delivery_fenced", id, { reason: a?.lease?.holder === "human" ? "human_control" : `task_${t?.state}` });
				} else {
					// Pi's input pipeline accepted it. Pi reports a delivery
					// mode only while streaming: "turn" means Pi was idle and
					// this starts its own turn.
					t.confirmed_at = now();
					t.accepted_as = e.streamingBehavior ?? "turn";
					saveTask(t);
					// Durable from here: Pi gets the prompt even if the
					// annotation cannot be written.
					annotate("task_started", id, { accepted_as: t.accepted_as });
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
			// A turn whose outcome is already frozen is not the human's to
			// mark; only a task still running in Pi is.
			// Only a turn Pi accepted can have been intervened in; a task handed
			// off but not yet accepted is put back unrun (#82).
			const running = receipt || pendingConfirm ? undefined : currentTask;
			try {
				withLock(() => {
					// Ownership first: the durable human lease is the fence,
					// and only its failure rejects the input. The annotations
					// below are best effort: once the lease is saved, failing
					// them must not lock the human out of their own Pi.
					setLeaseLocked("human", "human_input", "pi-tui");
					try {
						if (running) {
							const t = readJSONStrict(taskPath(running));
							if (t?.confirmed_at && !t.human_intervened) {
								t.human_intervened = true;
								saveTask(t);
							}
						}
						appendEvent("human_input", running, { bytes: e.text.length, during_task: !!running });
					} catch {}
				});
				humanClaimPending = undefined;
			} catch (err: any) {
				// Fail closed. Pi cleared the editor on submit; put the text
				// back (ahead of anything typed since) so nothing is lost, and
				// keep managed dispatch fenced until the claim is saved.
				humanClaimPending = "human_input";
				let restored = false;
				const uiCtx = ctx ?? ctxRef;
				try {
					if (uiCtx?.hasUI) {
						const cur = uiCtx.ui.getEditorText() ?? "";
						uiCtx.ui.setEditorText(cur ? `${e.text}\n${cur}` : e.text);
						restored = true;
					}
				} catch {}
				const imgs = e.images?.length ? " Attached images were not kept; re-attach them." : "";
				notify(`DevX could not record that you took control (${String(err?.message ?? err).slice(0, 120)}). Your message was not sent${restored ? " and is back in the editor" : ""}; queued tasks stay paused. Press Enter to retry.${imgs}`, "warning");
				statusLine();
				return { action: "handled" };
			}
			statusLine();
		}
		return { action: "continue" };
	});

	pi.on("agent_end", (e: any) => {
		if (currentTask && !receipt) lastEnd = lastAssistant(e.messages ?? []);
	});

	pi.on("agent_settled", (se: any) => {
		if (!currentTask || pendingConfirm || receipt) return;
		const end = lastEnd ?? { text: "" };
		// An abort that lands between turns (e.g. during a tool call) may
		// leave no aborted assistant message; the settle event still reports it.
		const aborted = end.stop === "aborted" || se?.aborted === true;
		// Protocol 2: "on request" only if this bridge dispatched the abort.
		// abortSent is set only after abort_sent_at was durably saved under
		// the lock and ctx.abort() called, so it alone is authoritative: no
		// read of durable task state, which a later cancel could change.
		settle({ text: end.text, stop: end.stop ?? "", error: end.error, aborted, abort_on_request: aborted && abortSent, abort_dispatched: abortSent });
		abortAttempt = undefined;
	});

	pi.registerCommand("devx-takeover", {
		description: "Take control of this DevX-managed agent (pauses queued remote tasks)",
		handler: async (_args, ctx) => {
			withLock(() => setLeaseLocked("human", "explicit_takeover", "pi-tui"));
			humanClaimPending = undefined;
			ctx.ui.notify("You have control. Queued DevX tasks are paused until /devx-release.", "info");
			statusLine();
		},
	});

	pi.registerCommand("devx-release", {
		description: "Release control back to DevX managed dispatch",
		handler: async (_args, ctx) => {
			withLock(() => setLeaseLocked("managed", "explicit_release", "pi-tui"));
			humanClaimPending = undefined;
			ctx.ui.notify("Released. DevX will deliver queued tasks when Pi is idle.", "info");
			statusLine();
		},
	});

	pi.registerCommand("devx-status", {
		description: "Show DevX managed-agent status",
		handler: async (_args, ctx) => {
			const a = readJSON(p.agent);
			const waiting = loadTasks().filter((t) => t.state === "waiting").length;
			const pending = receipt ? ` · result not yet saved: ${receipt.task_id} (${publishAttempts} attempts${publishError ? `; ${publishError}` : ""})` : "";
			const claim = humanClaimPending ? " · takeover not yet saved" : "";
			const rec = mode === "active" && !recovered ? ` · recovering an earlier session's result (${recoveryFailures} failed attempts)` : "";
			ctx.ui.notify(`agent ${AGENT_ID} · control: ${a?.lease?.holder} (gen ${a?.lease?.generation}) · running: ${currentTask ?? "none"} · waiting: ${waiting} · mode: ${mode}${pending}${claim}${rec}`, "info");
		},
	});

	pi.on("session_shutdown", () => {
		if (timer) clearInterval(timer);
		timer = undefined;
		if (mode === "stopped") return;
		const wasActive = mode === "active";
		mode = "stopped";
		// Last publication attempt. If it fails, the task is marked unknown as
		// before (devx would do the same once this bridge is gone), and the
		// spooled receipt lets the next instance attach the preserved result.
		if (receipt) tryPublish(true);
		try {
			withLock(() => {
				if (wasActive && humanClaimPending) {
					try {
						setLeaseLocked("human", humanClaimPending, "pi-tui");
					} catch {}
				}
				// A fenced hand-off Pi never ran goes back to waiting, not
				// to "unknown" when devx later sees this Pi gone. Best effort:
				// it must not stop the rest of shutdown.
				try {
					requeueLocked();
				} catch {}
				if (wasActive && currentTask) {
					const t = readJSON(taskPath(currentTask));
					if (t?.state === "running") {
						t.state = "unknown";
						t.finished_at = now();
						t.note = receipt ? "Pi exited before this task's settled result could be saved" : "Pi exited while this task was running";
						saveTask(t);
						appendEvent("task_unknown", t.id, { reason: t.note, ...(receipt ? { receipt_id: receipt.receipt_id, receipt_spooled: receiptSpooled } : {}) });
					}
				}
				const b = readJSON(p.bridge);
				if (b?.instance === instance) {
					b.mode = "stopped";
					b.heartbeat = now();
					writeJSON(p.bridge, b);
				}
				if (announced) appendEvent("bridge_offline", undefined, { instance });
			});
		} catch {}
	});
}
