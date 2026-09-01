import { readFile } from "node:fs/promises";
import { createBrowserWASI } from "./browser-wasi.mjs";
import { runNornRune } from "./harness.mjs";

const [modulePath, artifactPath, inputPath, expectedPath] = process.argv.slice(2);
if (!modulePath || !artifactPath || !inputPath || !expectedPath) {
  throw new Error("usage: browser-conformance.mjs MODULE ARTIFACT INPUT EXPECTED");
}

let instance;
const module = await WebAssembly.compile(await readFile(modulePath));
instance = await WebAssembly.instantiate(module, {
  wasi_snapshot_preview1: createBrowserWASI(() => instance.exports.memory),
});
instance.exports._initialize();

const actual = runNornRune(instance, await readFile(artifactPath), await readFile(inputPath));
const expected = await readFile(expectedPath);
if (!Buffer.from(actual).equals(expected)) {
  throw new Error("WebAssembly result differs from native result");
}
