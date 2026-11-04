import * as vscode from "vscode";
import { execFile } from "child_process";
import { access, constants, stat } from "fs/promises";
import * as path from "path";

const BINARY_NAME = process.platform === "win32" ? "guget.exe" : "guget";

export async function resolveGugetBinary(): Promise<string | undefined> {
  // 1. Explicit setting takes priority
  const configured = vscode.workspace
    .getConfiguration("guget")
    .get<string>("binaryPath", "");

  if (configured) {
    if (path.isAbsolute(configured) && await isExecutable(configured)) {
      return configured;
    }
    vscode.window.showWarningMessage(
      `guget.binaryPath "${configured}" must be an absolute path to an executable file. Correct or clear this setting to launch guget.`
    );
    return undefined;
  }

  // 2. Well-known install locations (match .scripts/install.ps1 and .scripts/install.sh)
  for (const candidate of wellKnownPaths()) {
    if (await isExecutable(candidate)) {
      return candidate;
    }
  }

  // 3. PATH lookup
  const fromPath = await findOnPath(BINARY_NAME);
  return fromPath && await isExecutable(fromPath) ? fromPath : undefined;
}

function wellKnownPaths(): string[] {
  const paths: string[] = [];
  if (process.platform === "win32") {
    const localAppData = process.env.LOCALAPPDATA;
    if (localAppData) {
      paths.push(path.join(localAppData, "Programs", "guget", "guget.exe"));
    }
  } else {
    paths.push("/usr/local/bin/guget");
    const home = process.env.HOME;
    if (home) {
      paths.push(path.join(home, ".local", "bin", "guget"));
    }
  }
  return paths;
}

async function isExecutable(filePath: string): Promise<boolean> {
  try {
    if (!(await stat(filePath)).isFile()) {
      return false;
    }
    await access(filePath, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function findOnPath(name: string): Promise<string | undefined> {
  const cmd = process.platform === "win32" ? "where" : "which";
  return new Promise((resolve) => {
    execFile(cmd, [name], (err, stdout) => {
      if (err || !stdout.trim()) {
        resolve(undefined);
      } else {
        resolve(stdout.trim().split(/\r?\n/)[0]);
      }
    });
  });
}
