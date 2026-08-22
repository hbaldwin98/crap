#!/usr/bin/env node

import { run } from "./crap-install-lib.mjs";

process.exitCode = run(process.argv.slice(2));
