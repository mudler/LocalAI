// SPDX-License-Identifier: MIT
package hostops

// Risk words of a host operation, for the dialogs that ask an administrator to
// allow it.
const (
	RiskRead        = "read"
	RiskChangesData = "changes data"
	RiskNetwork     = "network"
	RiskRunsJobs    = "runs jobs"
)

type description struct{ title, text, risk string }

var descriptions = map[string]description{
	"finetune.start":         {"Start fine-tuning", "Start fine-tuning jobs on this server's engines.", RiskRunsJobs},
	"finetune.stop":          {"Stop fine-tuning", "Stop fine-tuning jobs.", RiskRunsJobs},
	"finetune.get":           {"Read a fine-tuning job", "Read the state of a fine-tuning job.", RiskRead},
	"finetune.list":          {"List fine-tuning jobs", "List fine-tuning jobs.", RiskRead},
	"finetune.checkpoints":   {"List checkpoints", "Read the checkpoints of fine-tuning jobs.", RiskRead},
	"finetune.progress":      {"Read job progress", "Read the progress of fine-tuning jobs.", RiskRead},
	"finetune.dataset":       {"Prepare datasets", "Prepare datasets for fine-tuning.", RiskChangesData},
	"finetune.export":        {"Export a trained model", "Export a trained model into the model store.", RiskChangesData},
	"quantize.start":         {"Start quantization", "Start quantization jobs.", RiskRunsJobs},
	"quantize.stop":          {"Stop quantization", "Stop quantization jobs.", RiskRunsJobs},
	"quantize.get":           {"Read a quantization job", "Read the state of a quantization job.", RiskRead},
	"quantize.list":          {"List quantization jobs", "List quantization jobs.", RiskRead},
	"quantize.progress":      {"Read job progress", "Read the progress of quantization jobs.", RiskRead},
	"quantize.import":        {"Import a quantized model", "Import a quantized result into the model store.", RiskChangesData},
	"workspace.materialize":  {"Copy workspace files", "Copy files of a chat's workspace into the add-on's working area.", RiskChangesData},
	"workspace.commit":       {"Write results back", "Write results from the add-on's working area back to a chat's workspace.", RiskChangesData},
	"workspace.import":       {"Import workspace files", "Bring files into a chat's workspace.", RiskChangesData},
	"workspace.export":       {"Export workspace files", "Take files out of a chat's workspace.", RiskChangesData},
	"workspace.changes":      {"See workspace changes", "See which files of a chat's workspace changed.", RiskRead},
	"workspace.admin":        {"Check for an administrator", "Ask whether the current person is an administrator, for the add-on's settings.", RiskRead},
	"egress.check":           {"Check network access", "Ask the host whether a network connection is allowed.", RiskNetwork},
	"egress.audit":           {"Record network decisions", "Record the decision about a network connection in the audit log.", RiskNetwork},
	"connector.inbound":      {"Receive messages", "Let a connector hand incoming messages from outside to a chat.", RiskNetwork},
	"connector.file.put":     {"Receive files", "Move files from a connector into chats.", RiskChangesData},
	"connector.file.get":     {"Send files", "Move files from chats to a connector.", RiskChangesData},
	"client.jobs.start":      {"Start jobs for a client", "Start a saved task of the owner for a paired client.", RiskRunsJobs},
	"client.jobs.get":        {"Read a client's job", "Read the state and result of a job that a paired client started.", RiskRead},
	"client.jobs.cancel":     {"Cancel a client's job", "Cancel a job that a paired client started.", RiskRunsJobs},
	"client.workspace.list":  {"List a client's folders", "List the text files in the folders of your files that you gave to a paired client.", RiskRead},
	"client.workspace.read":  {"Read a client's folders", "Read text files from the folders of your files that you gave to a paired client.", RiskRead},
	"client.workspace.write": {"Write to a client's folder", "Save text files in the one folder of your files that you gave to a paired client to write.", RiskChangesData},
	"client.list":            {"List paired clients", "List the names of the paired clients that may use this add-on.", RiskRead},
}

// DescribeOperation gives the short title, one plain sentence and the risk word of a
// host operation, for the install dialog and the host access page. ok is
// false for a name the host does not register. A test fails when a supported
// operation has no description.
func DescribeOperation(op string) (title, text, risk string, ok bool) {
	d, ok := descriptions[op]
	return d.title, d.text, d.risk, ok
}
