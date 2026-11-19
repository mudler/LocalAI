---
description: Revoke P2P devices and invites, edit federation groups.
---
# Skill: Manage P2P Devices, Invites And Federation

1. Call `get_p2p_status`. If the mode is not `share`, say that devices and invites need the mode "Connect and share" (set on the Swarm, P2P page, with authentication on) and stop.
2. Call `list_p2p_devices` or `list_p2p_invites` and show the label, kind, state and how each device is connected. For a remote, say what it may use: all models (`all_models`) or the listed `models`. Never ask for or repeat an invite link: a person makes one on the Swarm, P2P page.
3. To revoke a device or cancel an invite, name it by label and ID, say that a revoked device must pair again with a new invite, and ask for confirmation under safety rule 1.
4. Call `revoke_p2p_device` or `cancel_p2p_invite` with the ID, only after the confirmation, then list again to show the new state.
5. On an error that names the P2P mode, tell the person; do not retry.

Federation groups (one model name served by this server and other LocalAI instances):

6. Call `list_federation`. Each instance lists its models; the `model` field is the name to use as a member. `limited` on a P2P path means a relay limits the connection.
7. To make a group, ask for the model name clients should use and pick at least two members that serve the same model. The routing is `least_busy` (default) or `round_robin`. After confirmation under safety rule 1, call `manage_federation_group` with action `create`; use `update` to change the routing or the members.
8. To take an instance out for maintenance, confirm, then call `manage_federation_group` with action `drain` and the member `model`; `undrain` puts it back. The last member in service cannot be drained. Action `delete` removes the group and keeps the members.
9. A person adds another instance as a remote on the Models, Remotes page; never ask for or repeat an invite link.
