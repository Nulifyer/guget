import * as vscode from "vscode";
import { execFile } from "child_process";
import { access, constants } from "fs/promises";
import * as path from "path";

const BINARY_NAME = process.platform === "win32" ? "guget.exe" : "guget";

export async function resolveGugetBinary(): Promise<string | undefined> {
  // 1. Explicit setting takes priority
  const configured = vscode.workspace
    .getConfiguration("guget")
    .get<string>("binaryPath", "");

  if (configured) {
    if (await isExecutable(configured)) {
      return configured;
    }
    vscode.window.showWarningMessage(
      `guget.binaryPath "${configured}" is not a valid executable. Falling back to PATH.`
    );
  }

  // 2. Well-known install locations (match install.ps1 / install.sh)
  for (const candidate of wellKnownPaths()) {
    if (await isExecutable(candidate)) {
      return candidate;
    }
  }

  // 3. PATH lookup
  return findOnPath(BINARY_NAME);
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
