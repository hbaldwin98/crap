import { spawnSync } from "node:child_process";
import { constants } from "node:os";
import { fileURLToPath } from "node:url";
import path from "node:path";

export function run(args, options = {}) {
  const spawn = options.spawn ?? spawnSync;
  const stderr = options.stderr ?? process.stderr;
  const root = options.root ?? path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const result = spawn("go", ["run", "./cmd/crap-install/main.go", ...args], {
    cwd: root,
    stdio: "inherit",
    shell: false,
  });

  if (result.error) {
    stderr.write(`crap installer: ${result.error.message}\n`);
    return 1;
  }
  if (result.status === null) {
	stderr.write(`crap installer: Go process ended without an exit status${result.signal ? ` (${result.signal})` : ""}\n`);
	return result.signal && constants.signals[result.signal] ? 128 + constants.signals[result.signal] : 1;
  }
  return result.status;
}
