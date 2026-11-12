import fs from "node:fs";
import path from "node:path";
import readline from "node:readline";

const [sourcePath, markdownPath, jsonlPath, mode] = process.argv.slice(2);
const includeTools = mode === "--include-tools";

if (!sourcePath || !markdownPath || !jsonlPath) {
  throw new Error("usage: node export-visible-chat.mjs <source.jsonl> <output.md> <output.jsonl>");
}

const redactionRules = [
  { name: "github_pat", pattern: /github_pat_[A-Za-z0-9_]+/g, replacement: "[已脱敏：GitHub PAT]" },
  { name: "github_token", pattern: /\bgh[pousr]_[A-Za-z0-9]{20,}\b/g, replacement: "[已脱敏：GitHub Token]" },
  { name: "bearer_token", pattern: /(Bearer\s+)[A-Za-z0-9._~+/=-]{20,}/gi, replacement: "$1[已脱敏：Bearer Token]" },
  { name: "query_secret", pattern: /([?&](?:token|access_token|api_key|key)=)[^&\s\"']+/gi, replacement: "$1[已脱敏]" },
  {
    name: "private_key",
    pattern: /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/g,
    replacement: "[已脱敏：私钥]",
  },
  {
    name: "binary_media",
    pattern: /data:(?:image|audio|video)\/[^;,\s]+;base64,[A-Za-z0-9+/=]+/g,
    replacement: "[二进制媒体已省略]",
  },
];

function redact(value) {
  let text = value;
  const applied = new Set();
  for (const rule of redactionRules) {
    rule.pattern.lastIndex = 0;
    if (rule.pattern.test(text)) {
      applied.add(rule.name);
      rule.pattern.lastIndex = 0;
      text = text.replace(rule.pattern, rule.replacement);
    }
  }
  return { text, applied: [...applied] };
}

function visibleContent(payload) {
  const texts = [];
  const attachments = [];
  for (const part of payload.content ?? []) {
    if ((part.type === "input_text" || part.type === "output_text") && typeof part.text === "string") {
      texts.push(part.text);
    } else if (part.type === "input_image") {
      attachments.push({ type: "image", note: "二进制图像未嵌入；文件路径或说明见相邻用户文本" });
    }
  }
  return { text: texts.join("\n"), attachments };
}

function stringifyVisible(value) {
  if (typeof value === "string") return value;
  if (value === undefined) return "";
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function markdownVisible(value) {
  return value.replace(/[ \t]+$/gm, "").replace(/\s+$/, "");
}

fs.mkdirSync(path.dirname(markdownPath), { recursive: true });
fs.mkdirSync(path.dirname(jsonlPath), { recursive: true });

const messages = [];
const input = fs.createReadStream(sourcePath, { encoding: "utf8" });
const lines = readline.createInterface({ input, crlfDelay: Infinity });

for await (const line of lines) {
  if (!line.trim()) continue;
  let item;
  try {
    item = JSON.parse(line);
  } catch {
    continue;
  }
  const payload = item.payload;
  if (item.type !== "response_item") continue;

  if (payload?.type === "message" && (payload.role === "user" || payload.role === "assistant")) {
    const content = visibleContent(payload);
    const redacted = redact(content.text);
    messages.push({
      sequence: messages.length + 1,
      timestamp: item.timestamp ?? null,
      event_type: "message",
      role: payload.role,
      phase: payload.role === "assistant" ? payload.phase ?? "unspecified" : "user_message",
      content: redacted.text,
      attachments: content.attachments,
      redactions: redacted.applied,
    });
    continue;
  }

  if (!includeTools) continue;

  let event = null;
  if (payload?.type === "function_call" || payload?.type === "custom_tool_call") {
    event = {
      event_type: "tool_call",
      role: "tool",
      phase: "visible_tool_call",
      tool_name: payload.namespace ? `${payload.namespace}.${payload.name}` : payload.name,
      call_id: payload.call_id ?? null,
      content: stringifyVisible(payload.arguments ?? payload.input),
    };
  } else if (payload?.type === "function_call_output" || payload?.type === "custom_tool_call_output") {
    event = {
      event_type: "tool_output",
      role: "tool",
      phase: "visible_tool_output",
      tool_name: null,
      call_id: payload.call_id ?? null,
      content: stringifyVisible(payload.output),
    };
  } else if (payload?.type === "agent_message") {
    event = {
      event_type: "agent_message",
      role: "agent",
      phase: "visible_agent_message",
      author: payload.author ?? null,
      recipient: payload.recipient ?? null,
      content: stringifyVisible(payload.content),
    };
  }

  if (event) {
    const redacted = redact(event.content);
    messages.push({
      sequence: messages.length + 1,
      timestamp: item.timestamp ?? null,
      ...event,
      content: redacted.text,
      attachments: [],
      redactions: redacted.applied,
    });
  }
}

const generatedAt = new Date().toISOString();
const sourceSession = path.basename(sourcePath).replace(/^rollout-/, "").replace(/\.jsonl$/, "");
const userCount = messages.filter((message) => message.role === "user").length;
const assistantCount = messages.filter((message) => message.role === "assistant").length;
const toolCallCount = messages.filter((message) => message.event_type === "tool_call").length;
const toolOutputCount = messages.filter((message) => message.event_type === "tool_output").length;
const agentMessageCount = messages.filter((message) => message.event_type === "agent_message").length;
const imageCount = messages.reduce((sum, message) => sum + message.attachments.length, 0);
const redactionCount = messages.reduce((sum, message) => sum + message.redactions.length, 0);

const markdown = [
  "# 题目 02 Codex 可见会话导出（脱敏）",
  "",
  `- 导出时间（UTC）：${generatedAt}`,
  `- 源会话：${sourceSession}`,
  `- 范围：${includeTools ? "user/assistant 可见消息、工具调用与输出、Agent 消息" : "仅 user 与 assistant 的可见消息"}；排除 system/developer 指令、隐藏推理和二进制图片`,
  `- 消息数：${messages.length}（用户 ${userCount}，助手 ${assistantCount}）`,
  `- 工具事件：调用 ${toolCallCount}，输出 ${toolOutputCount}；Agent 消息 ${agentMessageCount}`,
  `- 图片附件索引：${imageCount} 个（不嵌入 base64）`,
  `- 脱敏规则命中次数：${redactionCount}`,
  "- 重要说明：这是本机 Codex 会话日志的可见消息导出，不包含平台隐藏内容。",
  "",
];

for (const message of messages) {
  const roleLabel = message.role === "user" ? "用户" : message.role === "assistant" ? "助手" : message.role === "tool" ? "工具" : "Agent";
  markdown.push(`## ${String(message.sequence).padStart(4, "0")} · ${roleLabel} · ${message.timestamp ?? "时间不可获取"}`);
  markdown.push("");
  markdown.push(`- 事件类型：${message.event_type}`);
  markdown.push(`- 可见阶段：${message.phase}`);
  if (message.tool_name) markdown.push(`- 工具：${message.tool_name}`);
  if (message.call_id) markdown.push(`- 调用编号：${message.call_id}`);
  if (message.author || message.recipient) markdown.push(`- Agent：${message.author ?? "未知"} → ${message.recipient ?? "未知"}`);
  if (message.redactions.length > 0) {
    markdown.push(`- 脱敏：${message.redactions.join(", ")}`);
  }
  if (message.attachments.length > 0) {
    markdown.push(`- 附件：${message.attachments.length} 个图片（二进制未嵌入）`);
  }
  markdown.push("");
  markdown.push(markdownVisible(message.content) || "[无文本内容]");
  markdown.push("");
}

while (markdown.at(-1) === "") markdown.pop();
fs.writeFileSync(markdownPath, `${markdown.join("\n")}\n`, "utf8");
fs.writeFileSync(jsonlPath, messages.map((message) => JSON.stringify(message)).join("\n") + "\n", "utf8");

process.stdout.write(JSON.stringify({
  generatedAt,
  sourceSession,
  total: messages.length,
  users: userCount,
  assistants: assistantCount,
  toolCalls: toolCallCount,
  toolOutputs: toolOutputCount,
  agentMessages: agentMessageCount,
  images: imageCount,
  redactionRuleHits: redactionCount,
  markdownPath,
  jsonlPath,
}));
