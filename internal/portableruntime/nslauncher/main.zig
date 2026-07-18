const builtin = @import("builtin");
const std = @import("std");

pub const source_version = 2;
pub const required_zig_version = "0.16.0";

const protocol_environment = "FIZEAU_PORTABLE_NAMESPACE_PROTOCOL";
const protocol_version = 1;
const max_protocol_bytes = 1 << 20;
const max_arguments = 1024;
const max_environment = 1024;
const max_string_bytes = 4096;

const Launcher = struct {
    path: []const u8,
    digest: [32]u8,
};

const Target = struct {
    path: []const u8,
    args: []const []const u8,
    env: []const []const u8,
    dir: []const u8 = "",
};

// Protocol is intentionally a data-only copy of the lifecycle handoff.  The
// launcher has exactly one authority after decoding it: execve(target.path,
// target.args, target.env).  It neither consults PATH nor starts a wrapper.
const Protocol = struct {
    version: u32,
    uid: u32,
    gid: u32,
    launcher: Launcher,
    target: Target,
};

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

pub fn main(init: std.process.Init.Minimal) noreturn {
    const encoded_z = init.environ.getPosix(protocol_environment) orelse failClosed();
    const encoded = encoded_z[0..encoded_z.len];
    if (encoded.len == 0 or encoded.len > max_protocol_bytes) failClosed();

    const decoded_len = std.base64.url_safe_no_pad.Decoder.calcSizeForSlice(encoded) catch failClosed();
    if (decoded_len == 0 or decoded_len > max_protocol_bytes) failClosed();

    const allocator = std.heap.page_allocator;
    const decoded = allocator.alloc(u8, decoded_len) catch failClosed();
    std.base64.url_safe_no_pad.Decoder.decode(decoded, encoded) catch failClosed();

    const parsed = std.json.parseFromSlice(Protocol, allocator, decoded, .{
        .ignore_unknown_fields = false,
        .max_value_len = max_string_bytes,
    }) catch failClosed();
    defer parsed.deinit();
    const protocol = parsed.value;
    if (!validProtocol(protocol)) failClosed();

    execTarget(allocator, protocol.target) catch std.process.exit(126);
}

fn failClosed() noreturn {
    std.process.exit(125);
}

fn validProtocol(protocol: Protocol) bool {
    if (protocol.version != protocol_version or protocol.uid == 0 or protocol.gid == 0) return false;
    if (!validAbsolutePath(protocol.launcher.path) or !validAbsolutePath(protocol.target.path)) return false;
    if (std.mem.allEqual(u8, protocol.launcher.digest[0..], 0)) return false;
    if (protocol.target.dir.len != 0 and !validAbsolutePath(protocol.target.dir)) return false;
    if (protocol.target.args.len == 0 or protocol.target.args.len > max_arguments) return false;
    if (!std.mem.eql(u8, protocol.target.args[0], protocol.target.path)) return false;
    for (protocol.target.args) |argument| {
        if (!validString(argument)) return false;
    }
    if (protocol.target.env.len > max_environment) return false;
    for (protocol.target.env, 0..) |entry, i| {
        if (!validEnvironment(entry)) return false;
        for (protocol.target.env[0..i]) |previous| {
            const split = std.mem.indexOfScalar(u8, entry, '=') orelse return false;
            const previous_split = std.mem.indexOfScalar(u8, previous, '=') orelse return false;
            if (std.mem.eql(u8, entry[0..split], previous[0..previous_split])) return false;
        }
    }
    return true;
}

fn validAbsolutePath(path: []const u8) bool {
    if (path.len < 2 or path.len > max_string_bytes or path[0] != '/') return false;
    var components = std.mem.splitScalar(u8, path[1..], '/');
    while (components.next()) |component| {
        if (component.len == 0 or std.mem.eql(u8, component, ".") or std.mem.eql(u8, component, "..")) return false;
        if (!validString(component)) return false;
    }
    return true;
}

fn validString(value: []const u8) bool {
    return value.len <= max_string_bytes and std.mem.indexOfScalar(u8, value, 0) == null;
}

fn validEnvironment(entry: []const u8) bool {
    if (!validString(entry)) return false;
    const split = std.mem.indexOfScalar(u8, entry, '=') orelse return false;
    if (split == 0) return false;
    for (entry[0..split], 0..) |character, i| {
        if ((character >= 'A' and character <= 'Z') or (character >= 'a' and character <= 'z') or character == '_' or (i > 0 and character >= '0' and character <= '9')) continue;
        return false;
    }
    return true;
}

fn execTarget(allocator: std.mem.Allocator, target: Target) !noreturn {
    const argv = try allocator.allocSentinel(?[*:0]const u8, target.args.len, null);
    for (target.args, 0..) |argument, i| argv[i] = (try allocator.dupeZ(u8, argument)).ptr;
    const environment = try allocator.allocSentinel(?[*:0]const u8, target.env.len, null);
    for (target.env, 0..) |entry, i| environment[i] = (try allocator.dupeZ(u8, entry)).ptr;
    _ = std.os.linux.execve((try allocator.dupeZ(u8, target.path)).ptr, argv.ptr, environment.ptr);
    return error.ExecFailed;
}
