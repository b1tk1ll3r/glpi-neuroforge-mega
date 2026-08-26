# GLPI NeuroForge Mega v1.4.1

## Fix: Goal learning under Controlled Learning

- Adds `NEUROFORGE_GOAL_LEARNING_ENABLED` as an explicit, independent gate.
- Keeps the safe default `false`.
- Allows goal-cycle learning while `NEUROFORGE_CONTROLLED_LEARNING=true` remains enabled.
- Does not re-enable raw chat input learning, assistant-response learning, or imports.
- Wires the flag through Docker Compose and exposes it read-only in Control Center config.
