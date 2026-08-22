import assert from "node:assert/strict";
import test from "node:test";

import { run } from "./crap-install-lib.mjs";

test("runs the Go bootstrap and forwards arguments", () => {
  const calls = [];
  const status = run(["--client", "generic", "--dry-run"], {
    root: "project-root",
    spawn(command, args, options) {
      calls.push({ command, args, options });
      return { status: 0, signal: null };
    },
  });

  assert.equal(status, 0);
  assert.deepEqual(calls, [{
    command: "go",
    args: ["run", "./cmd/crap-install/main.go", "--client", "generic", "--dry-run"],
    options: { cwd: "project-root", stdio: "inherit", shell: false },
  }]);
});

test("returns the Go process exit status", () => {
  assert.equal(run([], { spawn: () => ({ status: 2, signal: null }) }), 2);
});

test("reports process launch failures", () => {
  let output = "";
  const status = run([], {
    spawn: () => ({ error: new Error("go not found") }),
    stderr: { write: (value) => { output += value; } },
  });

  assert.equal(status, 1);
  assert.match(output, /go not found/);
});

test("reports signal termination", () => {
  let output = "";
  const status = run([], {
    spawn: () => ({ status: null, signal: "SIGTERM" }),
    stderr: { write: (value) => { output += value; } },
  });

  assert.equal(status, 143);
  assert.match(output, /SIGTERM/);
});
