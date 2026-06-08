// Harness for the non-blocking Playwright smoke. Boots a real signal-server and
// two real Go nodes (host + viewer), generates a sample clip, and surfaces the
// bridge URLs + ids the spec needs. No mocks: this exercises the actual WebRTC /
// catalog / party path between two processes.
import { type ChildProcess, execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.resolve(HERE, '../..')
const BIN_NODE = path.join(REPO_ROOT, 'bin', 'node')
const BIN_SIGNAL = path.join(REPO_ROOT, 'bin', 'signal-server')
const SIGNAL_ADDR = '127.0.0.1:8080'
const SIGNALING_URL = `ws://${SIGNAL_ADDR}/ws`
const HOST_ADDR = '127.0.0.1:8801'
const VIEWER_ADDR = '127.0.0.1:8802'

export interface Cluster {
  hostURL: string
  viewerURL: string
  hostNodeId: string
  viewerNodeId: string
  contentId: string
  teardown: () => Promise<void>
}

// tokenFrom extracts the bearer token from a "...?token=<hex>" UI-ready URL.
export function tokenFrom(url: string): string {
  return new URL(url).searchParams.get('token') ?? ''
}

function ensureBinaries() {
  if (!existsSync(BIN_NODE)) {
    execFileSync('go', ['build', '-o', BIN_NODE, './cmd/node'], { cwd: REPO_ROOT, stdio: 'inherit' })
  }
  if (!existsSync(BIN_SIGNAL)) {
    execFileSync('go', ['build', '-o', BIN_SIGNAL, './cmd/signal-server'], { cwd: REPO_ROOT, stdio: 'inherit' })
  }
}

function generateClip(dir: string): Promise<void> {
  const out = path.join(dir, 'clip.mp4')
  return new Promise((resolve, reject) => {
    const ff = spawn('ffmpeg', [
      '-y',
      '-f', 'lavfi', '-i', 'testsrc=duration=2:size=320x240:rate=15',
      '-f', 'lavfi', '-i', 'sine=frequency=440:duration=2',
      '-c:v', 'libx264', '-c:a', 'aac', '-shortest', out,
    ], { stdio: 'ignore' })
    ff.on('error', reject)
    ff.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`ffmpeg exited ${code}`))))
  })
}

interface NodeInfo {
  proc: ChildProcess
  nodeId: string
  url: string
}

// spawnNode launches a node and resolves once it has printed "Node ID:" and
// "UI ready:". Stdout is buffered so callers can inspect it on failure.
function spawnNode(name: string, configDir: string, bridgeAddr: string): Promise<NodeInfo> {
  const proc = spawn(BIN_NODE, [
    '--name', name,
    '--config-dir', configDir,
    '--bridge-addr', bridgeAddr,
    '--no-open',
  ], { cwd: REPO_ROOT })

  let buf = ''
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${name} did not become ready in 30s\n${buf}`)), 30_000)
    let nodeId = ''
    let url = ''
    const onData = (chunk: Buffer) => {
      buf += chunk.toString()
      const idM = buf.match(/Node ID:\s*(\S+)/)
      if (idM) nodeId = idM[1]
      const urlM = buf.match(/UI ready:\s*(\S+)/)
      if (urlM) url = urlM[1]
      if (nodeId && url) {
        clearTimeout(timer)
        proc.stdout?.off('data', onData)
        resolve({ proc, nodeId, url })
      }
    }
    proc.stdout?.on('data', onData)
    proc.stderr?.on('data', (c: Buffer) => { buf += c.toString() })
    proc.on('error', (e) => { clearTimeout(timer); reject(e) })
    proc.on('exit', (code) => { clearTimeout(timer); reject(new Error(`${name} exited early (${code})\n${buf}`)) })
  })
}

function kill(proc?: ChildProcess) {
  if (proc && proc.exitCode === null) {
    try { proc.kill('SIGKILL') } catch { /* already gone */ }
  }
}

export async function startCluster(): Promise<Cluster> {
  ensureBinaries()

  const tmp = mkdtempSync(path.join(tmpdir(), 'p2p-smoke-'))
  const sampleDir = path.join(tmp, 'sample')
  const hostCfg = path.join(tmp, 'host')
  const viewerCfg = path.join(tmp, 'viewer')
  for (const d of [sampleDir, hostCfg, viewerCfg]) {
    execFileSync('mkdir', ['-p', d])
  }

  await generateClip(sampleDir)

  writeFileSync(path.join(hostCfg, 'config.toml'),
    `signaling_url = "${SIGNALING_URL}"\n`
    + `default_visibility = "public"\n`
    + `shared_folders = ["${sampleDir}"]\n`)
  writeFileSync(path.join(viewerCfg, 'config.toml'),
    `signaling_url = "${SIGNALING_URL}"\n`
    + `default_visibility = "public"\n`)

  const signal = spawn(BIN_SIGNAL, ['--addr', SIGNAL_ADDR], { cwd: REPO_ROOT })
  await new Promise((r) => setTimeout(r, 1000))

  let host: NodeInfo | undefined
  let viewer: NodeInfo | undefined
  const teardown = async () => {
    kill(host?.proc)
    kill(viewer?.proc)
    kill(signal)
    try { rmSync(tmp, { recursive: true, force: true }) } catch { /* best effort */ }
  }

  try {
    ;[host, viewer] = await Promise.all([
      spawnNode('Host', hostCfg, HOST_ADDR),
      spawnNode('Viewer', viewerCfg, VIEWER_ADDR),
    ])

    // Resolve the host's contentId via its own library API.
    const token = tokenFrom(host.url)
    const base = new URL(host.url).origin
    const res = await fetch(`${base}/api/library`, { headers: { Authorization: `Bearer ${token}` } })
    const titles = (await res.json()) as Array<{ contentId: string }>
    if (!titles.length) throw new Error('host library is empty; expected the sample clip')

    return {
      hostURL: host.url,
      viewerURL: viewer.url,
      hostNodeId: host.nodeId,
      viewerNodeId: viewer.nodeId,
      contentId: titles[0].contentId,
      teardown,
    }
  } catch (e) {
    await teardown()
    throw e
  }
}
