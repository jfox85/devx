// Test-only Pi extension: a deterministic local model so takeover tests run
// a real interactive Pi without credentials or network.
//   "SLOW"            -> long streamed reply (lets tests act mid-turn)
//   "FAIL"            -> provider error
//   "BIG:<n>"         -> reply of about n bytes
//   "SECRET"          -> reply containing a fake credential (redaction test)
// Every reply starts with "ECHO: <prompt>".
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { createFauxCore, fauxAssistantMessage } from "@earendil-works/pi-ai";

function lastUserText(context: any): string {
	let last = "";
	for (const m of context.messages ?? []) {
		if (m.role !== "user") continue;
		const c = m.content;
		last = typeof c === "string" ? c : c.filter((p: any) => p.type === "text").map((p: any) => p.text).join(" ");
	}
	return last;
}

export default function (pi: ExtensionAPI) {
	const core = createFauxCore({ provider: "devx-faux", models: [{ id: "faux-1" }], tokensPerSecond: 60 });
	// Unthrottled core for BIG replies, sharing the same api id.
	const fast = createFauxCore({ api: core.api, provider: "devx-faux", models: [{ id: "faux-1" }] });
	const respond = (context: any) => {
		const text = lastUserText(context);
		if (text.includes("FAIL")) return fauxAssistantMessage("", { stopReason: "error", errorMessage: "synthetic provider failure" });
		const big = /BIG:(\d+)/.exec(text);
		if (big) {
			const n = parseInt(big[1], 10);
			let body = "";
			for (let i = 0; body.length < n; i++) body += `line-${i} ` + "x".repeat(60) + "\n";
			return fauxAssistantMessage(`ECHO: ${text}\n${body}END-OF-BIG`);
		}
		if (text.includes("SECRET")) return fauxAssistantMessage(`ECHO: ${text} OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz0123456789`);
		const n = text.includes("SLOW") ? 400 : 3;
		return fauxAssistantMessage(`ECHO: ${text} ` + "word ".repeat(n).trim());
	};
	const refill = () => {
		core.appendResponses(Array.from({ length: 100 }, () => respond));
		fast.appendResponses(Array.from({ length: 100 }, () => respond));
	};
	refill();
	pi.registerProvider("devx-faux", {
		name: "devx faux",
		baseUrl: "http://127.0.0.1:9",
		apiKey: "unused",
		api: core.api as any,
		models: [{ id: "faux-1", name: "faux-1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 65536 }],
		streamSimple: (model: any, context: any, options: any) => {
			if (core.getPendingResponseCount() < 10 || fast.getPendingResponseCount() < 10) refill();
			return (/BIG:\d+/.test(lastUserText(context)) ? fast : core).streamSimple(model, context, options);
		},
	});
}
