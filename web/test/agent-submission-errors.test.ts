import { afterAll, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import type { CloudAgentChatMessage } from "../src/components/canvas/canvas-cloud-agent-chat-ui";

// Execute the production submission handler, isolating its React state, storage
// and network boundaries without global module mocks or a second implementation.
const sourceRoot = new URL("../src/", import.meta.url);
const source = readFileSync(new URL("components/canvas/canvas-cloud-agent-panel.tsx", sourceRoot), "utf8");
const start = source.indexOf("    const submit = async (");
const end = source.indexOf("\n    useEffect(() => {", start);
if (start < 0 || end < 0) throw new Error("Agent submission handler not found");
const dir = mkdtempSync(join(tmpdir(), "agent-submission-errors-"));
const modulePath = join(dir, "submit.ts");
const sourcePath = (path: string) => JSON.stringify(fileURLToPath(new URL(path, sourceRoot)));
writeFileSync(modulePath, `
import { appendAgentError, appendUniqueMessage, positiveNumber } from ${sourcePath("components/canvas/canvas-cloud-agent-events.ts")};
import { agentSubmissionErrorTitle } from ${sourcePath("lib/canvas/agent-error-presentation.ts")};
import { ApiError } from ${sourcePath("services/api/request.ts")};
import { CanvasSyncPendingError } from ${sourcePath("lib/canvas/sync-recovery.ts")};

export function fixture() {
    let messages = [];
    let prompt = "";
    let busy = false;
    let sequence = 0;
    const requestKeys = [];
    const canvasId = "canvas";
    const activeConversationId = "same-conversation";
    const conversationScope = "user:canvas:same-conversation";
    const currentScope = { current: conversationScope };
    const pendingSubmission = { current: null };
    const prefilledSubmissionRef = { current: null };
    const submissionRequestRef = { current: false };
    const canvasEditLeases = { agent: () => null, agentRejected: () => {} };
    const running = false, run = null, connectionStatus = "connected";
    const historyHydrated = true, pendingHydrated = true;
    const profileLoading = false, profileError = undefined;
    const profileView = { revision: "profile-revision" };
    const config = {}, selectedModel = "text-model", reasoningMode = "off";
    const selectedSkillIds = [], selectedNodeIds = [], installedSkills = [], conversations = [];
    const permissionMode = "request_approval", contextScope = ["canvas"];
    const maxCredits = "200", maxGenerationTasks = "0", maxVideoSeconds = "0";
    const setBusy = (value) => { busy = value; };
    const setPrompt = (value) => { prompt = value; };
    const setMessages = (value) => { messages = typeof value === "function" ? value(messages) : value; };
    const nanoid = () => "local-" + ++sequence;
    const getAgentCapabilities = async () => ({ permissionModes: [permissionMode], skills: true });
    const prepareCanvasForCloudAction = async () => {};
    const agentCustomModels = () => [];
    const agentModelSelection = () => ({ logicalModelId: selectedModel });
    const modelOptionName = (model) => model;
    const resolveSkillMentions = () => [];
    const agentRequestFingerprint = async () => "fingerprint";
    const waitForCanvasSync = (promise) => promise;
    const saveCloudAgentPendingSubmission = async () => {};
    const saveCloudAgentConversations = async () => {};
    const clearCloudAgentPendingSubmission = async () => {};
    const restoreAgentRequestModels = async (request) => request;
    const parseCloudAgentFormAnswer = () => null;
    const cloudAgentConversationTitle = () => "Conversation";
    const createAgentRun = async (request) => {
        requestKeys.push(request.idempotencyKey);
        throw new ApiError("自定义模型上游地址不可用或不允许访问", { status: 400 });
    };
    ${source.slice(start, end)}
    return { submit, requestKeys, messages: () => messages, prompt: () => prompt, busy: () => busy };
}
`);
const { fixture } = await import(modulePath) as {
    fixture: () => {
        submit: (prompt: string) => Promise<void>;
        requestKeys: string[];
        messages: () => CloudAgentChatMessage[];
        prompt: () => string;
        busy: () => boolean;
    };
};
afterAll(() => rmSync(dir, { recursive: true, force: true }));

test("each rejected submission in one conversation keeps its own visible error", async () => {
    const state = fixture();
    await state.submit("你好");
    await state.submit("你好");

    expect(state.requestKeys).toHaveLength(2);
    expect(new Set(state.requestKeys).size).toBe(2);
    expect(state.messages().map((item) => item.role)).toEqual(["user", "error", "user", "error"]);
    expect(state.messages().filter((item) => item.role === "error")).toEqual(state.requestKeys.map((key) => ({
        id: `submit-error-${key}`,
        role: "error",
        title: "请求已被服务端拒绝",
        text: "自定义模型上游地址不可用或不允许访问",
    })));
    expect(state.prompt()).toBe("你好");
    expect(state.busy()).toBe(false);
});
