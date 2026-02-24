import * as vscode from "vscode";
import { resolveGugetBinary } from "./gugetBinary";

export function activate(context: vscode.ExtensionContext) {
  const disposable = vscode.commands.registerCommand(
    "guget.manage",
    async () => {
      // 1. Determine target folder
      const targetFolder = await pickFolder();
      if (!targetFolder) {
        return;
      }

      // 2. Resolve binary
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

      // 3. Build arguments
      const config = vscode.workspace.getConfiguration("guget");
      const verbosity = config.get<string>("verbosity", "warn");
      const additionalArgs = config.get<string[]>("additionalArgs", []);

      const args: string[] = [
        "-p",
        targetFolder,
        "-v",
        verbosity,
        ...additionalArgs,
      ];

      // 4. Open guget as a fullscreen editor tab
      const terminal = vscode.window.createTerminal({
        name: `guget - ${folderBasename(targetFolder)}`,
        shellPath: binaryPath,
        shellArgs: args,
        location: vscode.TerminalLocation.Editor,
        iconPath: new vscode.ThemeIcon("package"),
        isTransient: true,
      });

      terminal.show();
    }
  );

  context.subscriptions.push(disposable);
}

export function deactivate() {}

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

function folderBasename(folderPath: string): string {
  const parts = folderPath.replace(/\\/g, "/").split("/");
  return parts[parts.length - 1] || folderPath;
}
