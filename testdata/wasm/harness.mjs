export function runNornRune(instance, artifact, input) {
  const api = instance.exports;
  const transfer = (bytes, operation) => {
    const pointer = api.nornrune_alloc(bytes.byteLength);
    if (pointer === 0) throw new Error(`${operation}: allocation failed`);
    new Uint8Array(api.memory.buffer, pointer, bytes.byteLength).set(bytes);
    const code = api[operation](pointer, bytes.byteLength);
    if (code !== 0) throw new Error(`${operation}: error ${code}`);
  };

  transfer(artifact, "nornrune_load_program");
  transfer(input, "nornrune_upload_input");
  if (api.nornrune_set_fuel(0xffffffffn) !== 0) throw new Error("set fuel failed");
  const evaluation = api.nornrune_evaluate();
  if (evaluation !== 0) throw new Error(`evaluate: error ${evaluation}`);
  const length = api.nornrune_result_length();
  const pointer = api.nornrune_alloc(length);
  if (pointer === 0 || api.nornrune_read_result(pointer, length) !== 0) {
    throw new Error("read result failed");
  }
  return new Uint8Array(api.memory.buffer.slice(pointer, pointer + length));
}
