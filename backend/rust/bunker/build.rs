fn main() {
    tonic_build::configure()
        .out_dir("src")
        .build_server(true)
        .build_client(false)
        .compile(
            &["../../../pkg/grpc/proto/backend.proto"], 
            &["../../../pkg/grpc/proto"])
        .expect("Failed to compile proto file");
}