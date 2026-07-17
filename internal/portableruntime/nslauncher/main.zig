const builtin = @import("builtin");
const std = @import("std");

pub const source_version = 1;
pub const required_zig_version = "0.16.0";

comptime {
    if (!std.mem.eql(u8, builtin.zig_version_string, required_zig_version)) {
        @compileError("Zig 0.16.0 is required");
    }
    if (builtin.os.tag != .linux) {
        @compileError("Linux is required");
    }
    if (builtin.abi != .musl) {
        @compileError("musl target is required");
    }
    if (builtin.cpu.arch != .x86_64 and builtin.cpu.arch != .aarch64) {
        @compileError("unsupported architecture");
    }
    if (builtin.link_mode != .static) {
        @compileError("static linking is required");
    }
    if (!builtin.single_threaded) {
        @compileError("single-threaded build is required");
    }
}

pub fn main() noreturn {
    std.process.exit(125);
}
