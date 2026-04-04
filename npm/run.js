#!/usr/bin/env node
'use strict';

const path = require('path');
const { execFileSync } = require('child_process');

const binary = process.env.ORCHARD_BINARY_PATH || path.join(__dirname, 'bin', 'orchard');

try {
  execFileSync(binary, process.argv.slice(2), { stdio: 'inherit' });
} catch (err) {
  if (err.code === 'ENOENT') {
    console.error(`orchard binary not found at ${binary}\nRun: npm rebuild git-orchard`);
  }
  process.exit(err.status || 1);
}
