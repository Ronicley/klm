import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type, type TSchema } from "typebox";
import { readFileSync } from "node:fs";

const linkedConfig = /*KLM_LINKED_CONFIG*/{} as {
  url: string; token: string; graphNode?: boolean; toolTimeoutMs: number;
  libraryUrl?: string; libraryToken?: string;
  tools?: { name: string; description: string; inputSchema: TSchema; external?: boolean }[];
};

// This gates agent-dispatched tools, not OS access or arbitrary extension code.
export default function (pi: ExtensionAPI) {
  let sealed = false;
  async function linkedRPC(method: string, params: unknown, signal?: AbortSignal, callId: string = crypto.randomUUID(), library = false) {
    // Readiness and graph gates retain their short timeout; tool calls can wait for a catalog.
    const external = method === "tools/call" && (linkedConfig.tools ?? []).some(tool => tool.external && tool.name === (params as { name?: string }).name);
    library ||= external;
    const timeout = AbortSignal.timeout(external ? 330000 : method === "tools/call" ? linkedConfig.toolTimeoutMs : 10000);
    const url = library ? linkedConfig.libraryUrl : linkedConfig.url;
    const token = library ? linkedConfig.libraryToken : linkedConfig.token;
    if (!url || !token) throw new Error("Library capability is unavailable.");
    const response = await fetch(url, {
      method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + token },
      body: JSON.stringify({ jsonrpc: "2.0", id: callId, method, params }),
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
    if (!response.ok) throw new Error("Linked-agent bridge is unavailable.");
    const envelope = await response.json() as { error?: unknown; result?: { batchID?: string; text?: string; content?: ({ type: "text"; text: string } | { type: "image"; data: string; mimeType: string })[]; structuredContent?: unknown; isError?: boolean; tools?: { name: string }[] } };
    if (envelope.error || !envelope.result) throw new Error("Invalid linked-agent bridge response.");
    return envelope.result;
  }
  const tools = [
    ...(linkedConfig.tools ?? []).map(tool => ({ name: tool.name, description: tool.description, parameters: tool.inputSchema, external: tool.external === true })),
  ];
  for (const tool of tools) {
    pi.registerTool({
      name: tool.name, description: tool.description, parameters: tool.parameters, label: tool.name.replaceAll("_", " "), executionMode: "sequential",
      async execute(toolCallId, params, signal) {
        if (sealed) throw new Error("Graph activation is sealed.");
        const result = await linkedRPC("tools/call", { name: tool.name, arguments: params }, signal, toolCallId);
        if (result.isError && !tool.external) throw new Error(result.content?.find(block => block.type === "text")?.text ?? "Linked-agent request failed.");
        if (linkedConfig.graphNode && tool.name === "graph_submit_choice") {
          const response = JSON.parse(result.content?.find(block => block.type === "text")?.text ?? "{}");
          sealed = response.reserved === true;
        }
        return { content: result.content ?? [], details: { structuredContent: result.structuredContent, mcpIsError: tool.external && result.isError === true } };
      },
    });
  }
  pi.on("tool_result", async (event) => {
    if (tools.some(tool => tool.external && tool.name === event.toolName) && (event.details as { mcpIsError?: boolean } | undefined)?.mcpIsError) {
      return { content: event.content, details: event.details, isError: true };
    }
  });
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
      // External tools use engine policy in their dispatch path, not this
      // privileged internal-tool bypass. The gateway validates the allowlist.
      if (event.toolName === "ask_user" || tools.some(tool => tool.name === event.toolName && !tool.external)) return;
      if (tools.some(tool => tool.name === event.toolName && tool.external)) return;
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
  pi.on("turn_end", async (_event, ctx) => {
    if (sealed) { ctx.abort(); return; }
    if (linkedConfig.graphNode || ctx.signal?.aborted) return;
    // Pull one combined input at the native boundary, without mutating Pi's
    // global steeringMode setting or racing an idle RPC steer command.
    const batch = await linkedRPC("klm/steering/take", {}, ctx.signal);
    if (batch.batchID && batch.text && !ctx.signal?.aborted) {
      pi.sendUserMessage(batch.content ?? batch.text, { deliverAs: "steer" });
    }
  });

  pi.on("before_agent_start", async (_event, ctx) => {
    if (linkedConfig.graphNode || ctx.signal?.aborted) return;
    const batch = await linkedRPC("klm/steering/take", {}, ctx.signal);
    if (batch.batchID && batch.text && !ctx.signal?.aborted) {
      return { message: { customType: "klm-steering", content: batch.content ?? batch.text, display: true } };
    }
  });

  pi.on("session_start", async (_, ctx) => {
    if (ctx.mode === "rpc" && ctx.hasUI) {
      const inventory = await linkedRPC("tools/list", {});
      const libraryInventory = tools.some(tool => tool.external) ? await linkedRPC("tools/list", {}, undefined, crypto.randomUUID(), true) : { tools: [] };
      if (!tools.every(tool => (tool.external ? libraryInventory : inventory).tools?.some(item => item.name === tool.name))) {
        throw new Error("Linked-agent tools are not ready.");
      }
      const allowed = new Set(["read", "write", "edit", "bash", "powershell", "grep", "find", "ls", "ask_user", ...tools.map(tool => tool.name)]);
      if (pi.getAllTools().some(tool => !allowed.has(tool.name))) throw new Error("Pi loaded unselected tools.");
      if (linkedConfig.graphNode) await linkedRPC("klm/graph/gate", {});
      ctx.ui.notify("klm.permissions.ready.v1", "info");
    }
  });
}
