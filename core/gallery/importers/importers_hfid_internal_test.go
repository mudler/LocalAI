package importers

import "testing"

func TestHuggingFaceRepoID(t *testing.T) {
	const want = "owner/Some-Model-GGUF"
	for _, in := range []string{
		"https://huggingface.co/owner/Some-Model-GGUF",
		"http://huggingface.co/owner/Some-Model-GGUF",
		"https://www.huggingface.co/owner/Some-Model-GGUF/",
		"https://huggingface.co/owner/Some-Model-GGUF/tree/main",
		"https://huggingface.co/owner/Some-Model-GGUF?show_file_info=a.gguf",
		"huggingface.co/owner/Some-Model-GGUF",
		"www.huggingface.co/owner/Some-Model-GGUF",
		"hf.co/owner/Some-Model-GGUF",
		"huggingface://owner/Some-Model-GGUF",
		"hf://owner/Some-Model-GGUF",
		"owner/Some-Model-GGUF",
		"  huggingface.co/owner/Some-Model-GGUF\n",
	} {
		if got := huggingFaceRepoID(in); got != want {
			t.Errorf("huggingFaceRepoID(%q) = %q, want %q", in, got, want)
		}
	}
}
