fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proto_path = std::env::var("BACKEND_PROTO_PATH")
        .unwrap_or_else(|_| "proto/backend.proto".to_string());

    let proto_dir = std::path::Path::new(&proto_path)
        .parent()
        .unwrap_or(std::path::Path::new("."));

    tonic_build::configure()
        .build_server(true)
        .build_client(false)
        .compile_protos(&[&proto_path], &[proto_dir])?;

    Ok(())
}
