// SPDX-License-Identifier: MIT
package cli

import (
	"fmt"
	"io"
	"os"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// AddonsCarrierKeysCMD makes the static key set that a split add-on owner and
// its frontends share.
type AddonsCarrierKeysCMD struct {
	Out       string   `required:"" type:"path" name:"out" help:"Directory to write the key set to. It must not hold any of the files yet"`
	OwnerHost []string `required:"" sep:"none" name:"owner-host" help:"DNS name or IP address of the add-on owner, as the frontends dial it. Repeat the flag for more than one"`

	// output receives the summary. It is stdout when nil.
	output io.Writer
}

func (c *AddonsCarrierKeysCMD) Run(ctx *cliContext.Context) error {
	if err := gateway.GenerateCarrierKeys(c.Out, c.OwnerHost); err != nil {
		return fmt.Errorf("--out: %w", err)
	}
	w := c.output
	if w == nil {
		w = os.Stdout
	}
	_, err := fmt.Fprintf(w, `Wrote the add-on carrier key set to %[1]s.

Mount on the add-on owner (--addons-role=owner), and nowhere else:
  ca.pem  owner.pem  owner.key  frontend.pem  frontend-sign.pub

Mount on every frontend (--addons-role=frontend):
  ca.pem  owner.pem  frontend.pem  frontend.key  frontend-sign.key

Point --addons-carrier-dir at the directory that holds them. Store the set as a
secret: the frontend files together let a holder act as a frontend. The CA key
was not kept.
`, c.Out)
	return err
}
