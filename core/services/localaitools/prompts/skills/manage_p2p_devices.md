---
description: Check the P2P state, list paired devices and invites, and revoke a device or cancel an invite.
---
# Skill: Manage P2P Devices And Invites

1. Call `get_p2p_status`. If the mode is not `share`, say that devices and invites need the mode "Connect and share" (set on the Swarm, P2P page, with authentication on) and stop.
2. Call `list_p2p_devices` or `list_p2p_invites` and show the label, kind, state and how each device is connected. Never ask for or repeat an invite link: a person makes one on the Swarm, P2P page.
3. To revoke a device or cancel an invite, name it by label and ID, say that a revoked device must pair again with a new invite, and ask for confirmation under safety rule 1.
4. Call `revoke_p2p_device` or `cancel_p2p_invite` with the ID, only after the confirmation, then list again to show the new state.
5. On an error that names the P2P mode, tell the person; do not retry.
