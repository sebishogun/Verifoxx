const ARTIFACT_HEADER_BYTES = 104;
const METADATA_BYTES = 128;

export function callWithBytes(instance, bytes, operation) {
  const api = instance.exports;
  const pointer = api.nornrune_alloc(bytes.byteLength);
  if (pointer === 0) throw new Error(`${operation}: allocation failed`);
  new Uint8Array(api.memory.buffer, pointer, bytes.byteLength).set(bytes);
  return api[operation](pointer, bytes.byteLength);
}

export function runNornRune(instance, artifact, input) {
  const api = instance.exports;
  const metadata = readMetadata(api);
  requireMatchingManifest(metadata, artifact);

  for (const [bytes, operation] of [
    [artifact, "nornrune_load_program"],
    [input, "nornrune_upload_input"],
  ]) {
    const code = callWithBytes(instance, bytes, operation);
    if (code !== 0) throw new Error(`${operation}: error ${code}`);
  }
  if (api.nornrune_set_fuel(metadata.maxFuel) !== 0) throw new Error("set fuel failed");
  const evaluation = api.nornrune_evaluate();
  if (evaluation !== 0) throw new Error(`evaluate: error ${evaluation}`);
  const length = api.nornrune_result_length();
  const pointer = api.nornrune_alloc(length);
  if (pointer === 0 || api.nornrune_read_result(pointer, length) !== 0) {
    throw new Error("read result failed");
  }
  return new Uint8Array(api.memory.buffer.slice(pointer, pointer + length));
}

function readMetadata(api) {
  if (api.nornrune_metadata_length() !== METADATA_BYTES) {
    throw new Error("metadata length mismatch");
  }
  const pointer = api.nornrune_alloc(METADATA_BYTES);
  if (pointer === 0 || api.nornrune_read_metadata(pointer, METADATA_BYTES) !== 0) {
    throw new Error("read metadata failed");
  }
  const view = new DataView(api.memory.buffer, pointer, METADATA_BYTES);
  if (view.getUint32(0, true) !== 0x4e52574d) throw new Error("metadata magic mismatch");
  return {
    abi: view.getUint16(4, true),
    schema: view.getUint16(6, true),
    profile: view.getUint8(8),
    capabilities: view.getUint32(12, true),
    maxArtifactBytes: view.getBigUint64(16, true),
    maxInputBytes: view.getBigUint64(24, true),
    maxOutputBytes: view.getBigUint64(32, true),
    maxFuel: view.getBigUint64(40, true),
    maxRows: view.getUint32(48, true),
    maxProgramColumns: view.getUint32(52, true),
  };
}

function requireMatchingManifest(metadata, artifact) {
  if (artifact.byteLength < ARTIFACT_HEADER_BYTES) throw new Error("artifact header is truncated");
  const view = new DataView(artifact.buffer, artifact.byteOffset, artifact.byteLength);
  if (view.getUint32(0, true) !== 0x4e525750) throw new Error("artifact magic mismatch");
  const fields = [
    ["ABI", view.getUint16(4, true), metadata.abi],
    ["schema", view.getUint16(6, true), metadata.schema],
    ["profile", view.getUint8(8), metadata.profile],
    ["capabilities", view.getUint32(12, true), metadata.capabilities],
    ["artifact bytes", view.getBigUint64(64, true), metadata.maxArtifactBytes],
    ["input bytes", view.getBigUint64(72, true), metadata.maxInputBytes],
    ["output bytes", view.getBigUint64(80, true), metadata.maxOutputBytes],
    ["fuel", view.getBigUint64(88, true), metadata.maxFuel],
    ["rows", view.getUint32(96, true), metadata.maxRows],
    ["Program columns", view.getUint32(100, true), metadata.maxProgramColumns],
  ];
  for (const [name, artifactValue, hostValue] of fields) {
    if (artifactValue !== hostValue) throw new Error(`${name} manifest mismatch`);
  }
}
