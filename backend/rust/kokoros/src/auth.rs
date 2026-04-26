use tonic::{Request, Status};

/// Returns an interceptor function if LOCALAI_GRPC_AUTH_TOKEN is set.
pub fn make_auth_interceptor(
) -> Option<impl Fn(Request<()>) -> Result<Request<()>, Status> + Clone> {
    let token = std::env::var("LOCALAI_GRPC_AUTH_TOKEN").ok()?;
    if token.is_empty() {
        return None;
    }
    let expected = format!("Bearer {}", token);
    Some(
        move |req: Request<()>| -> Result<Request<()>, Status> {
            let meta = req.metadata();
            match meta.get("authorization") {
                Some(val) => {
                    if val.as_bytes() == expected.as_bytes() {
                        Ok(req)
                    } else {
                        Err(Status::unauthenticated("invalid token"))
                    }
                }
                None => Err(Status::unauthenticated("missing authorization")),
            }
        },
    )
}
