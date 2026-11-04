const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "guget-vscode-host-"));
const workspace = path.join(temporary, "workspace");
fs.mkdirSync(workspace);
fs.writeFileSync(path.join(workspace, "App.csproj"), '<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>');
try {
  const resultPath = path.join(temporary, "result.json");
  const result = spawnSync(process.env.VSCODE_EXECUTABLE_PATH || "code", [
    "--new-window", "--wait", "--disable-gpu", "--no-sandbox",
    "--skip-welcome", "--skip-release-notes", "--disable-workspace-trust",
    `--user-data-dir=${path.join(temporary, "user-data")}`,
    `--extensions-dir=${path.join(temporary, "extensions")}`,
    `--extensionDevelopmentPath=${path.resolve(__dirname, "..")}`,
    `--extensionTestsPath=${path.join(__dirname, "host-smoke.cjs")}`,
    workspace,
  ], {
    stdio: "inherit", timeout: 120000,
    env: { ...process.env, GUGET_HOST_RESULT_PATH: resultPath },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    process.exitCode = result.status ?? 1;
  } else {
    const evidence = JSON.parse(fs.readFileSync(resultPath, "utf8"));
    console.log(`Extension-host smoke passed: VS Code ${evidence.vscodeVersion}; terminal lifecycle checked: ${evidence.terminalLifecycleChecked}`);
  }
} finally {
  fs.rmSync(temporary, { recursive: true, force: true });
}
