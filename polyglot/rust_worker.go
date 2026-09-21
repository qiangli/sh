package polyglot

// rustManifest is the Cargo manifest of the generated worker crate. The fence
// source is the crate root, so a fence uses serde exactly as any Rust program
// does (`use serde::{Serialize, Deserialize}` + `#[derive(...)]`); the
// dependencies are the standard serde toolchain, never a Bash#-specific
// carrier type. `[workspace]` keeps a temp directory under a user's checkout
// from being captured by that checkout's workspace. The binary name is unique
// per source (%s) so concurrent builds sharing one target directory never
// overwrite each other's output.
const rustManifest = `[package]
name = "bashpp_module"
version = "0.0.0"
edition = "2024"

[workspace]

[[bin]]
name = "%s"
path = "src/main.rs"

[dependencies]
serde = { version = "1", features = ["derive"] }
serde_json = "1"
base64 = "0.22"

[profile.dev]
opt-level = 0
debug = false
`

// rustWorkerRuntime is appended after a fence's own source. It turns the crate
// into the persistent worker Module.ensure launches: one JSON request per
// stdin line, one JSON response per line on fd 3 (Unix) or on the original
// stdout handle behind the "\x1eBASHPP" marker (Windows, where the parent
// cannot pass a fourth file). Values are serde_json::Value, so an ordinary
// `#[derive(Serialize, Deserialize)]` struct or Vec<Struct> crosses the Bash#
// Object mapping through serde_json::from_value / to_value; the generated
// __bpp_dispatch (see rustWorkerSource) is the only per-fence code.
//
// Island stdout/stderr are redirected to temporary files for the duration of
// each call and returned in the response, so the shell replays them and the
// protocol stream stays clean. Unix uses dup/dup2 on fds 1 and 2; Windows
// uses SetStdHandle, which Rust's std consults on every stdout/stderr write.
const rustWorkerRuntime = `
// ---- Bash# Rust worker runtime (generated; do not edit) ----
mod __bpp {
    use serde_json::Value;
    use std::io::{Read, Seek, Write};

    #[derive(serde::Deserialize)]
    pub struct Request {
        #[serde(default)]
        pub id: u64,
        pub op: String,
        #[serde(default)]
        pub name: String,
        #[serde(default)]
        pub args: Vec<Value>,
    }

    pub fn arity(func: &str, args: &[Value], want: usize) -> Result<(), String> {
        if args.len() != want {
            return Err(format!("Rust function {} expects {} arguments, got {}", func, want, args.len()));
        }
        Ok(())
    }
    pub fn arg<T: serde::de::DeserializeOwned>(func: &str, args: &[Value], i: usize) -> Result<T, String> {
        serde_json::from_value(args[i].clone()).map_err(|e| format!("Rust function {} argument {}: {}", func, i + 1, e))
    }
    pub fn bytes_arg(func: &str, args: &[Value], i: usize) -> Result<Vec<u8>, String> {
        use base64::Engine;
        if let Value::Object(map) = &args[i] {
            if let (1, Some(Value::String(text))) = (map.len(), map.get("$bytes")) {
                return base64::engine::general_purpose::STANDARD
                    .decode(text)
                    .map_err(|e| format!("Rust function {} argument {}: {}", func, i + 1, e));
            }
        }
        Err(format!("Rust function {} argument {}: expected bytes", func, i + 1))
    }
    pub fn bytes_value(data: &[u8]) -> Value {
        use base64::Engine;
        serde_json::json!({"$bytes": base64::engine::general_purpose::STANDARD.encode(data)})
    }
    pub fn value<T: serde::Serialize>(func: &str, value: T) -> Result<Value, String> {
        serde_json::to_value(value).map_err(|e| format!("Rust function {} result: {}", func, e))
    }

    #[cfg(unix)]
    mod os {
        unsafe extern "C" {
            fn dup(fd: i32) -> i32;
            fn dup2(old: i32, new: i32) -> i32;
            fn close(fd: i32) -> i32;
        }
        pub type Saved = (i32, i32);
        pub const MARKER: bool = false;
        pub fn protocol() -> std::fs::File {
            use std::os::unix::io::FromRawFd;
            unsafe { std::fs::File::from_raw_fd(3) }
        }
        pub fn redirect(out: &std::fs::File, err: &std::fs::File) -> Saved {
            use std::os::unix::io::AsRawFd;
            unsafe {
                let saved = (dup(1), dup(2));
                dup2(out.as_raw_fd(), 1);
                dup2(err.as_raw_fd(), 2);
                saved
            }
        }
        pub fn restore(saved: Saved) {
            unsafe {
                dup2(saved.0, 1);
                dup2(saved.1, 2);
                close(saved.0);
                close(saved.1);
            }
        }
    }
    #[cfg(windows)]
    mod os {
        use std::os::windows::io::{AsRawHandle, FromRawHandle};
        type Handle = *mut core::ffi::c_void;
        const STD_OUTPUT_HANDLE: u32 = -11i32 as u32;
        const STD_ERROR_HANDLE: u32 = -12i32 as u32;
        #[link(name = "kernel32")]
        unsafe extern "system" {
            fn GetStdHandle(id: u32) -> Handle;
            fn SetStdHandle(id: u32, handle: Handle) -> i32;
        }
        pub type Saved = (Handle, Handle);
        pub const MARKER: bool = true;
        pub fn protocol() -> std::fs::File {
            unsafe { std::fs::File::from_raw_handle(GetStdHandle(STD_OUTPUT_HANDLE)) }
        }
        pub fn redirect(out: &std::fs::File, err: &std::fs::File) -> Saved {
            unsafe {
                let saved = (GetStdHandle(STD_OUTPUT_HANDLE), GetStdHandle(STD_ERROR_HANDLE));
                SetStdHandle(STD_OUTPUT_HANDLE, out.as_raw_handle());
                SetStdHandle(STD_ERROR_HANDLE, err.as_raw_handle());
                saved
            }
        }
        pub fn restore(saved: Saved) {
            unsafe {
                SetStdHandle(STD_OUTPUT_HANDLE, saved.0);
                SetStdHandle(STD_ERROR_HANDLE, saved.1);
            }
        }
    }

    fn temp(seq: u64, tag: &str) -> std::io::Result<(std::fs::File, std::path::PathBuf)> {
        let path = std::env::temp_dir().join(format!("bashpp-rust-{}-{}-{}", std::process::id(), seq, tag));
        let file = std::fs::OpenOptions::new().read(true).write(true).create(true).truncate(true).open(&path)?;
        Ok((file, path))
    }
    fn drain(mut file: std::fs::File, path: std::path::PathBuf) -> String {
        let mut buf = Vec::new();
        let _ = file.seek(std::io::SeekFrom::Start(0));
        let _ = file.read_to_end(&mut buf);
        drop(file);
        let _ = std::fs::remove_file(path);
        String::from_utf8_lossy(&buf).into_owned()
    }
    fn guarded<F: FnOnce() -> Result<Value, String>>(call: F) -> Result<Value, String> {
        match std::panic::catch_unwind(std::panic::AssertUnwindSafe(call)) {
            Ok(result) => result,
            Err(payload) => Err(match payload.downcast_ref::<&str>() {
                Some(text) => format!("Rust function panicked: {}", text),
                None => match payload.downcast_ref::<String>() {
                    Some(text) => format!("Rust function panicked: {}", text),
                    None => "Rust function panicked".to_string(),
                },
            }),
        }
    }
    fn envelope_error(code: &str, message: String) -> Value {
        serde_json::json!({"code": code, "message": message})
    }
    pub fn capture<F: FnOnce() -> Result<Value, String>>(seq: u64, call: F) -> (Result<Value, String>, String, String) {
        let (out, out_path, err, err_path) = match (temp(seq, "out"), temp(seq, "err")) {
            (Ok((out, out_path)), Ok((err, err_path))) => (out, out_path, err, err_path),
            _ => return (guarded(call), String::new(), String::new()),
        };
        let _ = std::io::stdout().flush();
        let _ = std::io::stderr().flush();
        let saved = os::redirect(&out, &err);
        let result = guarded(call);
        let _ = std::io::stdout().flush();
        let _ = std::io::stderr().flush();
        os::restore(saved);
        (result, drain(out, out_path), drain(err, err_path))
    }

    pub fn serve(dispatch: fn(&str, &[Value]) -> Result<Value, String>) {
        use std::io::BufRead;
        std::panic::set_hook(Box::new(|_| {}));
        let mut protocol = std::mem::ManuallyDrop::new(os::protocol());
        let stdin = std::io::stdin();
        let mut seq = 0u64;
        for line in stdin.lock().lines() {
            let line = match line {
                Ok(line) => line,
                Err(_) => break,
            };
            if line.trim().is_empty() {
                continue;
            }
            seq += 1;
            let response = match serde_json::from_str::<Request>(&line) {
                Err(error) => serde_json::json!({"id": 0, "ok": false, "error": envelope_error("RUST-EWORKER-REQUEST", format!("invalid request: {}", error)), "stdout": "", "stderr": ""}),
                Ok(request) => match request.op.as_str() {
                    "load" => serde_json::json!({"id": request.id, "ok": true, "result": null, "stdout": "", "stderr": ""}),
                    "call" => {
                        let (result, out, err) = capture(seq, || dispatch(&request.name, &request.args));
                        match result {
                            Ok(value) => serde_json::json!({"id": request.id, "ok": true, "result": value, "stdout": out, "stderr": err}),
                            Err(message) => serde_json::json!({"id": request.id, "ok": false, "error": envelope_error("RUST-ECALL", message), "stdout": out, "stderr": err}),
                        }
                    }
                    other => serde_json::json!({"id": request.id, "ok": false, "error": envelope_error("RUST-EWORKER-OP", format!("unknown operation {}", other)), "stdout": "", "stderr": ""}),
                },
            };
            let mut frame = String::new();
            if os::MARKER {
                frame.push_str("\u{1e}BASHPP");
            }
            frame.push_str(&response.to_string());
            frame.push('\n');
            if protocol.write_all(frame.as_bytes()).and_then(|_| protocol.flush()).is_err() {
                break;
            }
        }
    }
}

fn main() {
    __bpp::serve(__bpp_dispatch);
}
`
