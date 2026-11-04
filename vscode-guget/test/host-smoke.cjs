const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");
const vscode = require("vscode");

exports.run = async function () {
  const extension = vscode.extensions.getExtension("nulifyer.guget");
  assert.ok(extension, "guget extension is installed in the development host");
  await extension.activate();
  assert.ok(extension.isActive);
  const commands = await vscode.commands.getCommands(true);
  assert.ok(commands.includes("guget.manage"));
  assert.ok(commands.includes("guget.manageProject"));
  const folder = vscode.workspace.workspaceFolders?.[0];
  assert.ok(folder, "smoke runner supplies a temporary workspace");
  const projects = await vscode.workspace.findFiles("**/*.csproj");
  assert.equal(projects.length, 1);

  // Real terminal creation needs a long-lived process with no guget-specific
  // dependencies. The platform-neutral mocked suite covers Windows ownership.
  if (process.platform !== "win32") {
    const temporary = await fs.mkdtemp(path.join(os.tmpdir(), "guget-host-process-"));
    const executable = path.join(temporary, "guget-test");
    const config = vscode.workspace.getConfiguration("guget");
    const original = config.get("binaryPath");
    const terminals = [];
    try {
      await fs.writeFile(executable, "#!/bin/sh\nwhile :; do sleep 1; done\n", { mode: 0o755 });
      await config.update("binaryPath", executable, vscode.ConfigurationTarget.Global);
      for (let i = 0; i < 2; i++) {
        const before = new Set(vscode.window.terminals);
        await vscode.commands.executeCommand("guget.manageProject", projects[0]);
        const terminal = vscode.window.terminals.find(item => !before.has(item));
        assert.ok(terminal, "command creates a real editor terminal");
        terminals.push(terminal);
      }
      for (const firstClosed of [1, 0]) {
        const terminal = terminals[firstClosed];
        const closed = new Promise(resolve => {
          const subscription = vscode.window.onDidCloseTerminal(item => {
            if (item === terminal) { subscription.dispose(); resolve(); }
          });
        });
        terminal.dispose();
        await closed;
        if (firstClosed === 1) {
          assert.ok(vscode.window.terminals.includes(terminals[0]), "closing B preserves A");
        }
      }
    } finally {
      terminals.forEach(terminal => terminal.dispose());
      await config.update("binaryPath", original, vscode.ConfigurationTarget.Global);
      await fs.rm(temporary, { recursive: true, force: true });
    }
  }
  assert.ok(process.env.GUGET_HOST_RESULT_PATH, "smoke runner supplies a result path");
  await fs.writeFile(process.env.GUGET_HOST_RESULT_PATH, JSON.stringify({
    vscodeVersion: vscode.version,
    terminalLifecycleChecked: process.platform !== "win32",
  }));
  console.log(`guget extension-host smoke passed on VS Code ${vscode.version}`);
};
