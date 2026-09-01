import { readFile } from "node:fs/promises";
import { WASI } from "node:wasi";
import { runNornRune } from "./harness.mjs";

const [modulePath, artifactPath, inputPath, expectedPath] = process.argv.slice(2);
if (!modulePath || !artifactPath || !inputPath || !expectedPath) {
  throw new Error("usage: conformance.mjs MODULE ARTIFACT INPUT EXPECTED");
}

const wasi = new WASI({ version: "preview1", args: [], env: {}, preopens: {} });
const moduleBytes = await readFile(modulePath);
const module = await WebAssembly.compile(moduleBytes);
const instance = await WebAssembly.instantiate(module, {
  wasi_snapshot_preview1: wasi.wasiImport,
});
wasi.initialize(instance);

const artifact = await readFile(artifactPath);
const input = await readFile(inputPath);
const expected = await readFile(expectedPath);
const actual = runNornRune(instance, artifact, input);
if (!Buffer.from(actual).equals(expected)) {
  throw new Error("WebAssembly result differs from native result");
}
