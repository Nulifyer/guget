import * as path from "path";
import * as vscode from "vscode";
import { resolveGugetBinary } from "./gugetBinary";

let statusBarItem: vscode.StatusBarItem | undefined;

export async function activate(context: vscode.ExtensionContext) {
  // Set context key for when clauses
  const hasDotnet = await checkForDotnetProjects();
  vscode.commands.executeCommand("setContext", "guget:hasDotnetProjects", hasDotnet);

  // Status bar item
  statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 0);
  statusBarItem.text = "$(package) guget";
  statusBarItem.tooltip = "Manage NuGet Packages";
  statusBarItem.command = "guget.manage";
  if (hasDotnet) {
    statusBarItem.show();
  }
  context.subscriptions.push(statusBarItem);

  // Watch for .csproj/.fsproj additions and deletions to update context dynamically
  const watcher = vscode.workspace.createFileSystemWatcher("**/*.{csproj,fsproj}");
  watcher.onDidCreate(() => updateDotnetContext(true));
  watcher.onDidDelete(async () => {
    const still = await checkForDotnetProjects();
    updateDotnetContext(still);
  });
  context.subscriptions.push(watcher);

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
}

export function deactivate() {}

// ─────────────────────────────────────────────
// Context key management
// ─────────────────────────────────────────────

async function checkForDotnetProjects(): Promise<boolean> {
  const files = await vscode.workspace.findFiles("**/*.{csproj,fsproj}", "**/node_modules/**", 1);
  return files.length > 0;
}

function updateDotnetContext(hasDotnet: boolean) {
  vscode.commands.executeCommand("setContext", "guget:hasDotnetProjects", hasDotnet);
  if (hasDotnet) {
    statusBarItem?.show();
  } else {
    statusBarItem?.hide();
  }
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
        vscode.Uri.parse("https://github.com/nulifyer/guget#installation")
      );
    }
    return;
  }

  const config = vscode.workspace.getConfiguration("guget");
  const verbosity = config.get<string>("verbosity", "warn");
  const theme = config.get<string>("theme", "auto");
  const additionalArgs = config.get<string[]>("additionalArgs", []);

  const args: string[] = [
    "-p",
    targetFolder,
    "-v",
    verbosity,
    "-t",
    theme,
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

  // Close the editor tab immediately when guget exits.
  const onClose = vscode.window.onDidCloseTerminal((t) => {
    if (t === terminal) {
      onClose.dispose();
      // The terminal process has ended — close its editor tab directly.
      // Find the tab backing this terminal and close it.
      for (const group of vscode.window.tabGroups.all) {
        for (const tab of group.tabs) {
          if (tab.input instanceof vscode.TabInputTerminal && tab.label === terminalName) {
            vscode.window.tabGroups.close(tab);
            return;
          }
        }
      }
    }
  });
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
