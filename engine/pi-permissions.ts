import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type, type TSchema } from "typebox";

const linkedConfig = /*KLM_LINKED_CONFIG*/{} as {
  url: string; token: string; graphNode?: boolean; toolTimeoutMs: number;
  tools?: { name: string; description: string; inputSchema: TSchema }[];
};

// This gates agent-dispatched tools, not OS access or arbitrary extension code.
export default function (pi: ExtensionAPI) {
  let sealed = false;
  async function linkedRPC(method: string, params: unknown, signal?: AbortSignal, callId: string = crypto.randomUUID()) {
    // Readiness and graph gates retain their short timeout; tool calls can wait for a catalog.
    const timeout = AbortSignal.timeout(method === "tools/call" ? linkedConfig.toolTimeoutMs : 10000);
    const response = await fetch(linkedConfig.url, {
      method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + linkedConfig.token },
      body: JSON.stringify({ jsonrpc: "2.0", id: callId, method, params }),
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
    if (!response.ok) throw new Error("Linked-agent bridge is unavailable.");
    const envelope = await response.json() as { error?: unknown; result?: { content?: { type: "text"; text: string }[]; isError?: boolean; tools?: { name: string }[] } };
    if (envelope.error || !envelope.result) throw new Error("Invalid linked-agent bridge response.");
    return envelope.result;
  }
  const tools = [
    ...(linkedConfig.tools ?? []).map(tool => ({ name: tool.name, description: tool.description, parameters: tool.inputSchema })),
  ];
  for (const tool of tools) {
    pi.registerTool({
      ...tool, label: tool.name.replaceAll("_", " "), executionMode: "sequential",
      async execute(toolCallId, params, signal) {
        if (sealed) throw new Error("Graph activation is sealed.");
        const result = await linkedRPC("tools/call", { name: tool.name, arguments: params }, signal, toolCallId);
        if (result.isError) throw new Error(result.content?.[0]?.text ?? "Linked-agent request failed.");
        if (linkedConfig.graphNode && tool.name === "graph_submit_choice") {
          const response = JSON.parse(result.content?.[0]?.text ?? "{}");
          sealed = response.reserved === true;
        }
        return { content: result.content ?? [], details: {} };
      },
    });
  }
  pi.registerTool({
    name: "ask_user",
    label: "Ask user",
    description: "Ask the user when you need a decision or missing information. Offer labeled choices when useful, or ask for free text. Do not use this tool to request permission to execute other tools.",
    parameters: Type.Object({
      questions: Type.Array(Type.Object({
        question: Type.String({ minLength: 1, maxLength: 16384 }),
        header: Type.Optional(Type.String({ maxLength: 256 })),
        options: Type.Optional(Type.Array(Type.Object({
          label: Type.String({ minLength: 1, maxLength: 512 }),
          description: Type.Optional(Type.String({ maxLength: 4096 })),
        }), { maxItems: 64 })),
        multiple: Type.Optional(Type.Boolean()),
        custom: Type.Optional(Type.Boolean()),
      }), { minItems: 1, maxItems: 12 }),
    }),
    executionMode: "sequential",
    async execute(toolCallId, params, signal, _onUpdate, ctx) {
      const dismissed = {
        content: [{ type: "text" as const, text: "User dismissed the question" }],
        details: {},
      };
      if (sealed || ctx.mode !== "rpc" || !ctx.hasUI || signal?.aborted) return dismissed;
      const payload = JSON.stringify({ toolCallId, cwd: ctx.cwd, questions: params.questions });
      if (payload.length > 262144) throw new Error("Question request is too large.");
      const value = await ctx.ui.input("klm.question.v1:" + payload, undefined, { signal });
      if (sealed || value === undefined || signal?.aborted) return dismissed;
      const answers: unknown = JSON.parse(value);
      if (!Array.isArray(answers) || answers.length !== params.questions.length ||
          answers.some((answer) => !Array.isArray(answer) || answer.length === 0 ||
            answer.some((text) => typeof text !== "string" || text.trim() === ""))) {
        throw new Error("Invalid question response.");
      }
      return {
        content: [{ type: "text", text: JSON.stringify(params.questions.map((question, index) => ({
          question: question.question,
          answers: answers[index],
        }))) }],
        details: {},
      };
    },
  });

  pi.on("tool_call", async (event, ctx) => {
    const denied = { block: true, reason: "Permission denied." };
    try {
      // Before internal-tool bypasses AND before remembered/native approvals.
      // The Choice's sequential execution mode serializes its entire tool batch.
      if (sealed) return { block: true, reason: "Graph activation is sealed.", terminate: true };
      if (linkedConfig.graphNode) await linkedRPC("klm/graph/gate", {}, ctx.signal);
      if (event.toolName === "ask_user" || tools.some(tool => tool.name === event.toolName)) return;
      const signal = ctx.signal;
      if (ctx.mode !== "rpc" || !ctx.hasUI || !signal || signal.aborted) {
        return denied;
      }
      const choice = await ctx.ui.select(
        "klm.permission.v1:" + JSON.stringify({
          toolCallId: event.toolCallId,
          toolName: event.toolName,
          cwd: ctx.cwd,
          input: event.input,
        }),
        ["Allow", "Deny"],
        { signal },
      );
      if (choice === "Allow" && !signal.aborted && !sealed) {
        if (linkedConfig.graphNode) await linkedRPC("klm/graph/gate", {}, signal);
        return;
      }
    } catch {
      // A missing UI, cancellation, or serialization failure must never allow execution.
    }
    return denied;
  });

  // At turn_end every tool result in this batch has entered native history.
  // Abort here, never from inside graph_submit_choice with a pending tool result.
  pi.on("turn_end", (_event, ctx) => {
    if (sealed) ctx.abort();
  });

  pi.on("session_start", async (_, ctx) => {
    if (ctx.mode === "rpc" && ctx.hasUI) {
      const inventory = await linkedRPC("tools/list", {});
      if (!tools.every(tool => inventory.tools?.some(item => item.name === tool.name))) {
        throw new Error("Linked-agent tools are not ready.");
      }
      if (linkedConfig.graphNode) await linkedRPC("klm/graph/gate", {});
      ctx.ui.notify("klm.permissions.ready.v1", "info");
    }
  });
}
