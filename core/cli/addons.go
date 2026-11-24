package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/packages"
)

// AddonsCMD works with add-on zip files, the ones `make addons-zip` writes.
type AddonsCMD struct {
	Inspect AddonsInspectCMD `cmd:"" help:"Check an add-on zip file and show what it holds, without installing it"`
	Install AddonsInstallCMD `cmd:"" help:"Upload an add-on zip file to a running server. You finish the install in the browser"`
}

// AddonsInspectCMD verifies a zip on this machine.
type AddonsInspectCMD struct {
	File string `arg:"" name:"file" type:"existingfile" help:"The add-on zip file"`

	// output receives the report. It is stdout when nil.
	output io.Writer
}

func (c *AddonsInspectCMD) Run(_ *cliContext.Context) error {
	sum, err := packages.InspectFile(context.Background(), c.File, 0, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", c.File, err)
	}
	w := c.output
	if w == nil {
		w = os.Stdout
	}
	writeAddonFileSummary(w, sum)
	return nil
}

func writeAddonFileSummary(w io.Writer, sum packages.FileSummary) {
	m := sum.Package
	_, _ = fmt.Fprintf(w, "add-on:      %s %s\n", sum.ID, sum.Version)
	_, _ = fmt.Fprintf(w, "platform:    %s/%s\n", sum.OS, sum.Arch)
	_, _ = fmt.Fprintf(w, "host API:    %d to %d\n", m.API.Min, m.API.Max)
	_, _ = fmt.Fprintf(w, "files:       %d, %d bytes\n", sum.Files, sum.Size)
	_, _ = fmt.Fprintf(w, "sha256:      %s\n", sum.SHA256)
	var req, opt []string
	if h := m.Capabilities.HostOperations; h != nil {
		req, opt = h.Required, h.Optional
	}
	_, _ = fmt.Fprintf(w, "host access: required %s; optional %s\n", listOrNone(req), listOrNone(opt))
	_, _ = fmt.Fprintln(w, "origin:      local file, unsigned. Nothing signs this file; install it only if you trust whoever made it.")
}

func listOrNone(l []string) string {
	if len(l) == 0 {
		return "none"
	}
	return strings.Join(l, ", ")
}

// AddonsInstallCMD uploads a zip to a running server.
type AddonsInstallCMD struct {
	File   string `arg:"" name:"file" type:"existingfile" help:"The add-on zip file"`
	Target string `env:"LOCALAI_TARGET,LOCALAI_MCP_TARGET" default:"http://localhost:8080" help:"Base URL of the LocalAI server"`
	APIKey string `env:"LOCALAI_API_KEY" help:"Administrator API key of the server, when the server asks for one"`

	output io.Writer
	client *http.Client
}

// The install itself is a decision about what the add-on may do on this host,
// so it is not made here. The command checks the file, hands it to the
// server's staging area and prints the page where the administrator confirms
// the install and the host operations.
func (c *AddonsInstallCMD) Run(_ *cliContext.Context) error {
	sum, err := packages.InspectFile(context.Background(), c.File, 0, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", c.File, err)
	}
	w := c.output
	if w == nil {
		w = os.Stdout
	}
	writeAddonFileSummary(w, sum)
	base, err := url.Parse(strings.TrimRight(c.Target, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return fmt.Errorf("--target: %q is not a http or https URL", c.Target)
	}
	view, err := c.upload(base)
	if err != nil {
		return err
	}
	if !strings.EqualFold(view.SHA256, sum.SHA256) {
		return errors.New("the server holds a file with another digest than the one sent; nothing was installed")
	}
	_, _ = fmt.Fprintf(w, "\nUploaded to %s as %s (%s).\n", base.Host, view.FileID, view.Relation)
	_, _ = fmt.Fprintf(w, "Finish the install in the browser, where you review the host access it asks for:\n  %s/app/addons?file=%s\n", base.String(), view.FileID)
	_, _ = fmt.Fprintln(w, "The file waits there for one hour.")
	return nil
}

func (c *AddonsInstallCMD) upload(base *url.URL) (addons.FileView, error) {
	var view addons.FileView
	f, err := os.Open(c.File) // #nosec G304 -- the file the administrator named on the command line
	if err != nil {
		return view, err
	}
	defer func() { _ = f.Close() }()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, e := mw.CreateFormFile("file", filepath.Base(c.File))
		if e == nil {
			_, e = io.Copy(part, f)
		}
		if e == nil {
			e = mw.Close()
		}
		_ = pw.CloseWithError(e)
	}()
	req, err := http.NewRequest(http.MethodPost, base.String()+"/api/addons/files", pr)
	if err != nil {
		return view, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return view, fmt.Errorf("upload to %s: %w", base.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			return view, fmt.Errorf("the server refused the file (%d %s): %s", resp.StatusCode, e.Error.Type, e.Error.Message)
		}
		return view, fmt.Errorf("the server refused the file (%d)", resp.StatusCode)
	}
	if err = json.NewDecoder(bytes.NewReader(body)).Decode(&view); err != nil || view.FileID == "" {
		return view, errors.New("the server answered with something that is not an add-on file")
	}
	return view, nil
}
