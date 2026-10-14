// SPDX-License-Identifier: MIT
package packaging

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/go-containerregistry/pkg/v1"
)

var ownerRepoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// catalogRelease is the shape of one catalog entry the host reads. It repeats
// the JSON names of catalog.Release on purpose: the generated file is checked
// against the names, not against this program's idea of them.
type catalogRelease struct {
	AddonID     string `json:"addonId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Channel     string `json:"channel"`
	Artifact    string `json:"artifact"`
	Digest      string `json:"digest"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Policy      struct {
		RequireSignature bool   `json:"requireSignature"`
		Issuer           string `json:"issuer"`
		Identity         string `json:"identity"`
	} `json:"policy"`
	UI struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		MainPage    string `json:"mainPage"`
		Icon        string `json:"icon"`
	} `json:"ui"`
}

// signerIdentity is the exact certificate identity of the add-on package
// workflow for one ref. It names this workflow file and no other, so a
// signature of the backend workflows never satisfies an add-on policy.
func signerIdentity(ownerRepo, ref string) (string, error) {
	if !ownerRepoRE.MatchString(ownerRepo) {
		return "", fmt.Errorf("owner/repository %q is invalid", ownerRepo)
	}
	if err := validRef(ref); err != nil || !(strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/tags/")) {
		return "", fmt.Errorf("signer ref %q is invalid", ref)
	}
	return "https://github.com/" + ownerRepo + "/.github/workflows/" + SignerWorkflow + "@" + ref, nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// cmdCatalog renders the catalog entries of one merged layout from a
// template. Every value comes from a verified layout or an explicit input;
// nothing is looked up from a registry, so the entries pin the digests that
// were built and verified.
func cmdCatalog(args []string) error {
	fs := flag.NewFlagSet("catalog", flag.ContinueOnError)
	template := fs.String("template", "gallery/addons/tables.json.tmpl", "entry template")
	dir := fs.String("merged", "", "merged layout")
	config := fs.String("config", "", "platform matrix file")
	repository := fs.String("repository", "", "registry and repository the package was published to")
	tag := fs.String("tag", "", "immutable tag the artifact reference names: the version or sha-<commit>")
	channel := fs.String("channel", "", "channel name the entries are current for: latest, development or a branch channel")
	ownerRepo := fs.String("owner-repo", "", "GitHub owner/repository that ran the workflow")
	signerRef := fs.String("signer-ref", "", "ref of the signing workflow run")
	out := fs.String("out", "", "output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := parseRepository(*repository)
	if err != nil {
		return err
	}
	if !immutableTagRE.MatchString(*tag) {
		return fmt.Errorf("--tag %q is not an immutable version or commit tag", *tag)
	}
	if *dir == "" || *out == "" || *channel == "" {
		return errors.New("--merged, --channel and --out are required")
	}
	if *channel != aliasLatest && *channel != aliasDevelopment && !branchAliasRE.MatchString(*channel) {
		return fmt.Errorf("channel %q is not a channel alias", *channel)
	}
	identity, err := signerIdentity(*ownerRepo, *signerRef)
	if err != nil {
		return err
	}
	tmpl, err := os.ReadFile(*template)
	if err != nil {
		return err
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	m, _, err := verifyMerged(*dir, cfg)
	if err != nil {
		return err
	}
	root, err := readRootIndex(*dir)
	if err != nil {
		return err
	}
	raw, err := readBlob(*dir, root.Manifests[0].Digest, root.Manifests[0].Size)
	if err != nil {
		return err
	}
	var im v1.IndexManifest
	if err := json.Unmarshal(raw, &im); err != nil {
		return err
	}
	var releases []catalogRelease
	for _, d := range im.Manifests {
		l, err := loadLegByDescriptor(*dir, d)
		if err != nil {
			return err
		}
		platform := d.Platform.OS + "/" + d.Platform.Architecture
		var rec Receipt
		for _, r := range m.Legs {
			if r.Platform == platform {
				rec = r
			}
		}
		v, err := verifyLoaded(l, &rec, platform, m.Version, cfg)
		if err != nil {
			return err
		}
		ui := v.Manifest.Capabilities.UI
		title, description, mainPage, icon := v.Manifest.ID, "", "", ""
		if ui != nil {
			mainPage, icon = ui.MainPage, ui.Icon
			if ui.Title != nil {
				title = ui.Title.Default()
			}
			if ui.Description != nil {
				description = ui.Description.Default()
			}
		}
		values := map[string]string{
			"addon": v.Manifest.ID, "version": m.Version, "tag": *tag, "channel": *channel,
			"repository": repo.Name(), "digest": d.Digest.String(),
			"os": d.Platform.OS, "arch": d.Platform.Architecture,
			"issuer": OIDCIssuer, "identity": identity,
			"title": title, "description": description, "main_page": mainPage, "icon": icon,
		}
		text := string(tmpl)
		for k, val := range values {
			text = strings.ReplaceAll(text, "{{"+k+"}}", jsonString(val))
		}
		if strings.Contains(text, "{{") {
			return fmt.Errorf("template has an unknown placeholder near %q", text[strings.Index(text, "{{"):min(len(text), strings.Index(text, "{{")+40)])
		}
		var r catalogRelease
		if err := strictJSON([]byte(text), &r); err != nil {
			return fmt.Errorf("rendered entry: %w", err)
		}
		if err := checkRelease(r, m.Version, d.Digest.String(), repo.Name(), *tag); err != nil {
			return err
		}
		releases = append(releases, r)
	}
	b, err := json.MarshalIndent(releases, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "entries=%d\nchannel=%s\n", len(releases), *channel)
	return nil
}

// checkRelease is the template contract: an entry pins the digest that was
// built, names the package repository, and requires a signature of the
// add-on workflow.
func checkRelease(r catalogRelease, version, digest, repository, tag string) error {
	switch {
	case r.Digest != digest || !digestRE.MatchString(r.Digest):
		return errors.New("entry digest differs from the verified image digest")
	case r.Version != version:
		return errors.New("entry version differs from the package version")
	case r.Artifact != "oci://"+repository+":"+tag:
		return fmt.Errorf("entry artifact %q must be oci://%s:%s", r.Artifact, repository, tag)
	case !r.Policy.RequireSignature || r.Policy.Issuer != OIDCIssuer:
		return errors.New("entry must require a signature of the GitHub Actions OIDC issuer")
	case !strings.Contains(r.Policy.Identity, "/.github/workflows/"+SignerWorkflow+"@refs/"):
		return errors.New("entry identity must name the add-on package workflow")
	case r.OS == "" || r.Arch == "" || r.AddonID == "" || r.Channel == "":
		return errors.New("entry needs addon id, os, arch and channel")
	}
	return nil
}
