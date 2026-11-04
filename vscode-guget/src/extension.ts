import * as path from "path";
import * as vscode from "vscode";
import { resolveGugetBinary } from "./gugetBinary";

export async function activate(context: vscode.ExtensionContext) {
  const statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 0);
  statusBarItem.text = "$(package) guget";
  statusBarItem.tooltip = "Manage NuGet Packages";
  statusBarItem.command = "guget.manage";
  statusBarItem.hide();
  context.subscriptions.push(statusBarItem);

  let disposed = false;
  let generation = 0;
  let scanning = false;
  let scanPromise: Promise<void>;

  // Only publish a scan if no newer filesystem or workspace event invalidated it.
  // A burst of events requests one follow-up scan instead of concurrent scans.
  async function scanProjects() {
    try {
      while (!disposed) {
        const scanGeneration = generation;
        let hasDotnet: boolean;
        try {
          hasDotnet = await checkForDotnetProjects();
        } catch (error) {
          if (scanGeneration !== generation) {
            continue;
          }
          console.warn("guget: could not scan workspace projects", error);
          return;
        }
        if (disposed) {
          return;
        }
        if (scanGeneration !== generation) {
          continue;
        }
        await vscode.commands.executeCommand("setContext", "guget:hasDotnetProjects", hasDotnet);
        if (disposed) {
          return;
        }
        if (scanGeneration !== generation) {
          continue;
        }
        if (hasDotnet) {
          statusBarItem.show();
        } else {
          statusBarItem.hide();
        }
        return;
      }
    } catch (error) {
      console.warn("guget: could not update workspace project context", error);
    } finally {
      scanning = false;
    }
  }

  function refreshProjects(): Promise<void> {
    generation++;
    if (!scanning) {
      scanning = true;
      scanPromise = scanProjects();
    }
    return scanPromise;
  }

  context.subscriptions.push({ dispose() { disposed = true; } });
  const watcher = vscode.workspace.createFileSystemWatcher("**/*.{csproj,fsproj,vbproj}");
  context.subscriptions.push(
    watcher,
    watcher.onDidCreate(refreshProjects),
    watcher.onDidDelete(refreshProjects),
    vscode.workspace.onDidChangeWorkspaceFolders(refreshProjects)
  );

  // guget.manage — command palette / keybinding / status bar
  context.subscriptions.push(
    vscode.commands.registerCommand("guget.manage", async () => {
      const targetFolder = await pickFolder();
      if (!targetFolder) {
        return;
      }
      await launchGuget(context, targetFolder);
    })
  );

  // guget.manageProject — explorer context menu / editor title button
  context.subscriptions.push(
    vscode.commands.registerCommand("guget.manageProject", async (uri?: vscode.Uri) => {
      if (!uri) {
        return;
      }
      const projectDir = path.dirname(uri.fsPath);
      await launchGuget(context, projectDir);
    })
  );

  // Register event handlers before starting discovery so activation cannot miss a save.
  await refreshProjects();
}

export function deactivate() {}

// ─────────────────────────────────────────────
// Context key management
// ─────────────────────────────────────────────

async function checkForDotnetProjects(): Promise<boolean> {
  const files = await vscode.workspace.findFiles("**/*.{csproj,fsproj,vbproj}", "**/node_modules/**", 1);
  return files.length > 0;
}

// ─────────────────────────────────────────────
// Launch guget
// ─────────────────────────────────────────────

async function launchGuget(context: vscode.ExtensionContext, targetFolder: string) {
  const binaryPath = await resolveGugetBinary();
  if (!binaryPath) {
    const action = await vscode.window.showErrorMessage(
      "guget binary not found. Install it or set guget.binaryPath in settings.",
      "Open Settings",
      "Installation Guide"
    );
    if (action === "Open Settings") {
      vscode.commands.executeCommand(
        "workbench.action.openSettings",
        "guget.binaryPath"
      );
    } else if (action === "Installation Guide") {
      vscode.env.openExternal(
        vscode.Uri.parse("https://github.com/nulifyer/guget/blob/main/docs/installation.md")
      );
    }
    return;
  }

  const config = vscode.workspace.getConfiguration("guget");
  const verbosity = config.get<string>("verbosity", "warn");
  const theme = config.get<string>("theme", "auto");
  const sortField = config.get<string>("sortField", "status");
  const sortDirection = config.get<string>("sortDirection", "asc");
  const additionalArgs = config.get<string[]>("additionalArgs", []);

  const args: string[] = [
    "-p",
    targetFolder,
    "-v",
    verbosity,
    "-t",
    theme,
    "-o",
    `${sortField}:${sortDirection}`,
    ...additionalArgs,
  ];

  const terminalName = "guget";

  const terminal = vscode.window.createTerminal({
    name: terminalName,
    shellPath: binaryPath,
    shellArgs: args,
    location: vscode.TerminalLocation.Editor,
    iconPath: vscode.Uri.file(
      path.join(context.extensionPath, "icon.svg")
    ),
    isTransient: true,
  });

  terminal.show(false);
  // Editor-location terminals may not receive keyboard focus automatically;
  // explicitly focus the active terminal so keystrokes go to guget immediately.
  vscode.commands.executeCommand("workbench.action.terminal.focus");

  // VS Code removes the editor tab when its terminal closes. Tab labels are
  // shared by every guget session and cannot identify a session's terminal.
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

async function pickFolder(): Promise<string | undefined> {
  const folders = vscode.workspace.workspaceFolders;

  if (!folders || folders.length === 0) {
    const picked = await vscode.window.showOpenDialog({
      canSelectFolders: true,
      canSelectFiles: false,
      canSelectMany: false,
      openLabel: "Select .NET project folder",
    });
    return picked?.[0]?.fsPath;
  }

  if (folders.length === 1) {
    return folders[0].uri.fsPath;
  }

  const chosen = await vscode.window.showWorkspaceFolderPick({
    placeHolder: "Select workspace folder to scan for NuGet packages",
  });
  return chosen?.uri.fsPath;
}
