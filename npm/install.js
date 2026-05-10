#!/usr/bin/env node
'use strict';

const https = require('https');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { execFileSync } = require('child_process');
const os = require('os');

// Allow pre-installed binary via env var
if (process.env.ORCHARD_BINARY_PATH) {
  console.log('ORCHARD_BINARY_PATH set — skipping download.');
  process.exit(0);
}

const REPO = 'drewdrewthis/git-orchard-rs';
const pkg = JSON.parse(fs.readFileSync(path.join(__dirname, 'package.json'), 'utf8'));
const version = pkg.version;

function detectTarget() {
  const platform = os.platform();
  const arch = os.arch();

  if (platform === 'darwin') {
    return arch === 'arm64' ? 'aarch64-apple-darwin' : 'x86_64-apple-darwin';
  }

  if (platform === 'linux') {
    return arch === 'arm64' ? 'aarch64-unknown-linux-gnu' : 'x86_64-unknown-linux-gnu';
  }

  throw new Error(
    `Unsupported platform: ${platform}/${arch}.\n` +
    `Please build from source: cargo install --git https://github.com/${REPO}`
  );
}

const ALLOWED_HOSTS = new Set([
  'github.com',
  'objects.githubusercontent.com',
  'github-releases.githubusercontent.com',
  'release-assets.githubusercontent.com',
]);

const MAX_REDIRECTS = 5;

function download(url, redirectCount = 0) {
  return new Promise((resolve, reject) => {
    if (redirectCount > MAX_REDIRECTS) {
      reject(new Error(`Too many redirects fetching ${url}`));
      return;
    }

    const proxy = process.env.HTTPS_PROXY || process.env.https_proxy;
    let req;

    if (proxy) {
      const proxyUrl = new URL(proxy);
      const targetUrl = new URL(url);
      const targetPort = targetUrl.port || '443';
      const net = require('net');
      const socket = net.connect(
        parseInt(proxyUrl.port || '443', 10),
        proxyUrl.hostname,
        () => {
          socket.write(
            `CONNECT ${targetUrl.hostname}:${targetPort} HTTP/1.1\r\nHost: ${targetUrl.hostname}:${targetPort}\r\n\r\n`
          );
        }
      );
      socket.once('data', () => {
        req = https.get({ hostname: targetUrl.hostname, path: targetUrl.pathname + targetUrl.search, socket, agent: false }, handleResponse);
      });
      socket.on('error', reject);
    } else {
      req = https.get(url, handleResponse);
    }

    function handleResponse(res) {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        const redirectHost = new URL(res.headers.location).hostname;
        if (!ALLOWED_HOSTS.has(redirectHost)) {
          reject(new Error(`Redirect to untrusted host: ${redirectHost}`));
          return;
        }
        resolve(download(res.headers.location, redirectCount + 1));
        return;
      }
      if (res.statusCode !== 200) {
        reject(new Error(`HTTP ${res.statusCode} fetching ${url}`));
        return;
      }
      const chunks = [];
      res.on('data', chunk => chunks.push(chunk));
      res.on('end', () => resolve(Buffer.concat(chunks)));
      res.on('error', reject);
    }

    if (req) req.on('error', reject);
  });
}

async function main() {
  let target;
  try {
    target = detectTarget();
  } catch (err) {
    console.error(`Error: ${err.message}`);
    process.exit(1);
  }

  const baseUrl = `https://github.com/${REPO}/releases/download/orchard-v${version}`;
  const tarball = `orchard-${target}.tar.gz`;
  const checksumFile = `${tarball}.sha256`;

  console.log(`Downloading orchard v${version} for ${target}...`);

  let tarData, checksumData;
  try {
    [tarData, checksumData] = await Promise.all([
      download(`${baseUrl}/${tarball}`),
      download(`${baseUrl}/${checksumFile}`),
    ]);
  } catch (err) {
    console.error(
      `Failed to download orchard binary: ${err.message}\n` +
      `You can install from source instead:\n  cargo install --git https://github.com/${REPO}`
    );
    process.exit(1);
  }

  // Verify checksum
  const expectedHash = checksumData.toString('utf8').trim().split(/\s+/)[0];
  const actualHash = crypto.createHash('sha256').update(tarData).digest('hex');
  if (actualHash !== expectedHash) {
    console.error(
      `Checksum mismatch!\n  expected: ${expectedHash}\n  got:      ${actualHash}\n` +
      `Install from source: cargo install --git https://github.com/${REPO}`
    );
    process.exit(1);
  }

  // Extract binary
  const binDir = path.join(__dirname, 'bin');
  fs.mkdirSync(binDir, { recursive: true });

  const tmpTar = path.join(os.tmpdir(), tarball);
  fs.writeFileSync(tmpTar, tarData);

  try {
    execFileSync('tar', ['xzf', tmpTar, '-C', binDir, 'orchard']);
  } catch (err) {
    console.error(
      `Failed to extract binary: ${err.message}\n` +
      `Install from source: cargo install --git https://github.com/${REPO}`
    );
    process.exit(1);
  } finally {
    fs.unlinkSync(tmpTar);
  }

  fs.chmodSync(path.join(binDir, 'orchard'), 0o755);
  console.log('orchard installed successfully.');
}

main();
