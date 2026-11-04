const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");

const bundlePath = path.resolve(__dirname, "../dist/extension.js");
const bundle = fs.readFileSync(bundlePath, "utf8");
const binary = process.execPath;
const project = { fsPath: path.join(path.sep, "work", "App.csproj") };

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

// Mock public VS Code and filesystem APIs around the real built extension.
// This checks command/lifecycle logic, not native terminal or editor rendering.
function session({ configured = binary, folders = [{ uri: { fsPath: path.dirname(project.fsPath) } }] } = {}) {
  const commands = new Map();
  const terminals = [];
  const closedTabs = [];
  const contexts = [];
  const warnings = [];
  const errors = [];
  const scanWarnings = [];
  const listeners = { create: [], delete: [], folders: [], close: [] };
  const subscriptions = [];
  const files = new Map([[binary, { file: true, executable: true }]]);
  let scan = async () => [project];
  let scanCalls = 0;
  let pathCalls = 0;
  let pathOutput = "";
  let configuration = configured;
  class TabInputTerminal {}
  function on(event) {
    return callback => {
      const listener = { callback, active: true, dispose() { this.active = false; } };
      listeners[event].push(listener);
      return listener;
    };
  }
  function emit(event, value) {
    return Promise.all(listeners[event].filter(l => l.active).map(l => l.callback(value)));
  }
  const status = {
    visible: false,
    show() { this.visible = true; },
    hide() { this.visible = false; },
    dispose() { this.disposed = true; },
  };
  const vscode = {
    TabInputTerminal,
    StatusBarAlignment: { Left: 1 },
    TerminalLocation: { Editor: 1 },
    Uri: { file: fsPath => ({ fsPath }), parse: value => ({ value }) },
    workspace: {
      workspaceFolders: folders,
      findFiles: (...args) => { scanCalls++; return scan(...args); },
      getConfiguration: () => ({ get: (key, fallback) => key === "binaryPath" ? configuration : fallback }),
      createFileSystemWatcher: () => ({ onDidCreate: on("create"), onDidDelete: on("delete"), dispose() {} }),
      onDidChangeWorkspaceFolders: on("folders"),
    },
    commands: {
      registerCommand(name, callback) { commands.set(name, callback); return { dispose() {} }; },
      async executeCommand(...args) { if (args[0] === "setContext") contexts.push(args[2]); },
    },
    window: {
      createStatusBarItem: () => status,
      createTerminal(options) { const terminal = { options, show() {} }; terminals.push(terminal); return terminal; },
      onDidCloseTerminal: on("close"),
      tabGroups: { all: [{ tabs: [] }], close: tab => { closedTabs.push(tab); } },
      showErrorMessage: async message => { errors.push(message); },
      showWarningMessage: async message => { warnings.push(message); },
      showWorkspaceFolderPick: async () => undefined,
      showOpenDialog: async () => undefined,
    },
    env: { openExternal() {} },
  };
  const fakeFs = {
    constants: fs.constants,
    async stat(filePath) {
      const item = files.get(filePath);
      if (!item) throw new Error("ENOENT");
      return { isFile: () => item.file };
    },
    async access(filePath) {
      if (!files.get(filePath)?.executable) throw new Error("EACCES");
    },
  };
  const module = { exports: {} };
  function localRequire(name) {
    if (name === "vscode") return vscode;
    if (name === "fs/promises") return fakeFs;
    if (name === "child_process") return {
      execFile(_command, _args, callback) { pathCalls++; callback(null, pathOutput); },
    };
    return require(name);
  }
  vm.runInThisContext("(function(require,module,exports,console){" + bundle + "\n})", { filename: bundlePath })(
    localRequire, module, module.exports, { warn: (...args) => scanWarnings.push(args) }
  );
  return {
    vscode, commands, terminals, closedTabs, contexts, warnings, errors, status, files, scanWarnings,
    get scanCalls() { return scanCalls; },
    get pathCalls() { return pathCalls; },
    activate: () => module.exports.activate({ extensionPath: path.dirname(bundlePath), subscriptions }),
    dispose: () => subscriptions.forEach(item => item.dispose()),
    setScan: callback => { scan = callback; },
    setConfigured: value => { configuration = value; },
    setPath: value => { pathOutput = value; },
    create: () => emit("create"), delete: () => emit("delete"), foldersChanged: () => emit("folders"),
    close: terminal => emit("close", terminal),
  };
}

for (const firstClosed of [0, 1]) {
  for (const removeClosedTabFirst of [true, false]) {
    test(`closing session ${firstClosed + 1} preserves the other session (tab removed first: ${removeClosedTabFirst})`, async () => {
      const s = session();
      await s.activate();
      await s.commands.get("guget.manage")();
      await s.commands.get("guget.manage")();
      const tabs = s.terminals.map(() => ({ input: new s.vscode.TabInputTerminal(), label: "guget" }));
      s.vscode.window.tabGroups.all[0].tabs = removeClosedTabFirst ? [tabs[1 - firstClosed]] : tabs;
      await s.close(s.terminals[firstClosed]);
      assert.equal(s.closedTabs.length, 0);
      await s.close(s.terminals[1 - firstClosed]);
      assert.equal(s.closedTabs.length, 0);
    });
  }
}

test("stale delete scan cannot hide a newly created project, and event bursts coalesce", async () => {
  const s = session();
  await s.activate();
  const pending = deferred();
  let calls = 0;
  s.setScan(() => ++calls === 1 ? pending.promise : Promise.resolve([project]));
  const deletion = s.delete();
  const creation = s.create();
  const folderChange = s.foldersChanged();
  pending.resolve([]);
  await Promise.all([deletion, creation, folderChange]);
  assert.equal(calls, 2);
  assert.equal(s.contexts.at(-1), true);
  assert.equal(s.contexts.includes(false), false);
  assert.equal(s.status.visible, true);
});

test("workspace-folder removal triggers discovery and hides project controls", async () => {
  const s = session();
  await s.activate();
  s.vscode.workspace.workspaceFolders = [];
  s.setScan(async () => []);
  await s.foldersChanged();
  assert.equal(s.contexts.at(-1), false);
  assert.equal(s.status.visible, false);
});

test("events during activation invalidate its initial scan", async () => {
  const s = session();
  const initial = deferred();
  let calls = 0;
  s.setScan(() => ++calls === 1 ? initial.promise : Promise.resolve([project]));
  const activation = s.activate();
  const creation = s.create();
  initial.resolve([]);
  await Promise.all([activation, creation]);
  assert.equal(calls, 2);
  assert.equal(s.contexts.at(-1), true);
  assert.equal(s.contexts.includes(false), false);
});

test("failed scans retain the last context and later events recover", async () => {
  const s = session();
  await s.activate();
  s.setScan(async () => { throw new Error("workspace temporarily unavailable"); });
  await s.delete();
  assert.equal(s.contexts.at(-1), true);
  assert.equal(s.scanWarnings.length, 1);
  s.setScan(async () => []);
  await s.delete();
  assert.equal(s.contexts.at(-1), false);
});

test("disposing during discovery prevents a late context or status update", async () => {
  const s = session();
  await s.activate();
  const pending = deferred();
  s.setScan(() => pending.promise);
  const deletion = s.delete();
  s.dispose();
  pending.resolve([]);
  await deletion;
  assert.deepEqual(s.contexts, [true]);
});

for (const [name, configured, entry] of [
  ["directory", path.dirname(binary), { file: false, executable: true }],
  ["missing file", path.join(path.sep, "missing", "guget"), undefined],
  ["non-executable file", path.join(path.sep, "not-executable", "guget"), { file: true, executable: false }],
  ["relative file", "relative-guget", { file: true, executable: true }],
]) {
  test(`invalid explicit binaryPath (${name}) prevents launch and fallback`, async () => {
    const s = session({ configured });
    if (entry) s.files.set(configured, entry);
    s.setPath(binary);
    await s.activate();
    await s.commands.get("guget.manage")();
    assert.equal(s.terminals.length, 0);
    assert.equal(s.pathCalls, 0);
    assert.equal(s.warnings.length, 1);
    assert.equal(s.errors.length, 1);
  });
}

test("an explicit executable file wins without a PATH lookup", async () => {
  const s = session();
  await s.activate();
  await s.commands.get("guget.manage")();
  assert.equal(s.terminals[0].options.shellPath, binary);
  assert.equal(s.pathCalls, 0);
});

test("an empty binaryPath allows a validated PATH executable", async () => {
  const s = session({ configured: "" });
  s.setPath(binary + "\n");
  await s.activate();
  await s.commands.get("guget.manage")();
  assert.equal(s.terminals[0].options.shellPath, binary);
  assert.equal(s.pathCalls, 1);
});

test("standard installation locations keep precedence over PATH", async () => {
  const s = session({ configured: "" });
  const standard = process.platform === "win32"
    ? path.join(process.env.LOCALAPPDATA || "", "Programs", "guget", "guget.exe")
    : "/usr/local/bin/guget";
  if (process.platform === "win32" && !process.env.LOCALAPPDATA) return;
  s.files.set(standard, { file: true, executable: true });
  s.setPath(binary);
  await s.activate();
  await s.commands.get("guget.manage")();
  assert.equal(s.terminals[0].options.shellPath, standard);
  assert.equal(s.pathCalls, 0);
});

test("PATH results must also be executable files", async () => {
  const s = session({ configured: "" });
  const directory = path.dirname(binary);
  s.files.set(directory, { file: false, executable: true });
  s.setPath(directory);
  await s.activate();
  await s.commands.get("guget.manage")();
  assert.equal(s.terminals.length, 0);
});

test("cancelling the multi-root picker launches no process", async () => {
  const s = session({ folders: [{ uri: { fsPath: "/A" } }, { uri: { fsPath: "/B" } }] });
  await s.activate();
  await s.commands.get("guget.manage")();
  assert.equal(s.terminals.length, 0);
});

test("spaced shell-special project paths remain one process argument", async () => {
  const s = session();
  await s.activate();
  const directory = path.join(path.sep, "work", "Project one;$HOME`whoami`");
  await s.commands.get("guget.manageProject")({ fsPath: path.join(directory, "App.csproj") });
  assert.deepEqual(s.terminals[0].options.shellArgs, ["-p", directory, "-v", "warn", "-t", "auto", "-o", "status:asc"]);
});
