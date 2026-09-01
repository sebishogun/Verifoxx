const errnoBadFileDescriptor = 8;
const fileTypeCharacterDevice = 2;

export function createBrowserWASI(memory) {
  const view = () => new DataView(memory().buffer);
  const write32 = (pointer, value) => view().setUint32(pointer, value, true);
  let lastMonotonic = 0n;

  const imports = {
    args_get: () => 0,
    args_sizes_get: (count, bytes) => (write32(count, 0), write32(bytes, 0), 0),
    environ_get: () => 0,
    environ_sizes_get: (count, bytes) => (write32(count, 0), write32(bytes, 0), 0),
    clock_time_get: (clock, _precision, output) => {
      let nanoseconds;
      if (clock === 0) {
        nanoseconds = BigInt(Date.now()) * 1000000n;
      } else {
        nanoseconds = BigInt(Math.max(1, Math.trunc(performance.now() * 1000000)));
        if (nanoseconds <= lastMonotonic) nanoseconds = lastMonotonic + 1n;
        lastMonotonic = nanoseconds;
      }
      view().setBigUint64(output, nanoseconds, true);
      return 0;
    },
    random_get: (pointer, length) => {
      const bytes = new Uint8Array(memory().buffer, pointer, length);
      for (let offset = 0; offset < length; offset += 65536) {
        crypto.getRandomValues(bytes.subarray(offset, Math.min(offset + 65536, length)));
      }
      return 0;
    },
    sched_yield: () => 0,
    fd_close: (fd) => (fd <= 2 ? 0 : errnoBadFileDescriptor),
    fd_fdstat_get: (fd, output) => {
      if (fd > 2) return errnoBadFileDescriptor;
      new Uint8Array(memory().buffer, output, 24).fill(0);
      view().setUint8(output, fileTypeCharacterDevice);
      return 0;
    },
    fd_fdstat_set_flags: (fd) => (fd <= 2 ? 0 : errnoBadFileDescriptor),
    fd_prestat_get: () => errnoBadFileDescriptor,
    fd_prestat_dir_name: () => errnoBadFileDescriptor,
    fd_write: (_fd, iovectors, count, written) => {
      let bytes = 0;
      for (let index = 0; index < count; index++) bytes += view().getUint32(iovectors + index * 8 + 4, true);
      write32(written, bytes);
      return 0;
    },
    poll_oneoff: (_input, _output, _count, events) => (write32(events, 0), 0),
    proc_exit: (code) => { throw new Error(`WASI exit ${code}`); },
  };

  return new Proxy(imports, {
    get: (target, name) => {
      if (Object.hasOwn(target, name)) return target[name];
      throw new Error(`unsupported WASI import ${String(name)}`);
    },
  });
}
