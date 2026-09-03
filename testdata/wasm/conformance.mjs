import { readFile } from "node:fs/promises";
import { WASI } from "node:wasi";
import { callWithBytes, runNornRune } from "./harness.mjs";

const [modulePath, artifactPath, inputPath, expectedPath, mismatchPath] = process.argv.slice(2);
if (!modulePath || !artifactPath || !inputPath || !expectedPath || !mismatchPath) {
  throw new Error("usage: conformance.mjs MODULE ARTIFACT INPUT EXPECTED MISMATCHED_ARTIFACT");
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
if (callWithBytes(instance, await readFile(mismatchPath), "nornrune_load_program") !== 2) {
  throw new Error("module accepted mismatched artifact limits");
}
const actual = runNornRune(instance, artifact, input);
if (!Buffer.from(actual).equals(expected)) {
  throw new Error("WebAssembly result differs from native result");
}
