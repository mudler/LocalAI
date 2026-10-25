package messaging

// SubjectNodeBackendInstallProgress returns the NATS subject for transient
// progress events emitted by a worker during a single backend.install run.
// Per-op so multiple concurrent installs on the same node never alias.
func SubjectNodeBackendInstallProgress(nodeID, opID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.install." + sanitizeSubjectToken(opID) + ".progress"
}
