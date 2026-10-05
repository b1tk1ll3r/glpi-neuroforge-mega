variable "IMAGE_TAG" {
  default = "1.6.2"
}

variable "REGISTRY" {
  default = "git.send.nrw/sendnrw"
}

# Comma-separated additional tags, e.g. "latest" for main-branch builds.
variable "EXTRA_TAGS" {
  default = ""
}

# OCI source label; GHCR uses it to link the packages to the repository.
variable "SOURCE_URL" {
  default = ""
}

function "tags" {
  params = [name]
  result = concat(
    ["${REGISTRY}/glpi-neuroforge-mega-${name}:${IMAGE_TAG}"],
    [for t in compact(split(",", EXTRA_TAGS)) : "${REGISTRY}/glpi-neuroforge-mega-${name}:${trimspace(t)}"]
  )
}

group "default" {
  targets = ["neuroforge", "neuroforge-worker", "agent", "agent-data-init", "knowledge", "control"]
}

target "_common" {
  platforms = ["linux/amd64"]
  labels = {
    "org.opencontainers.image.source"  = SOURCE_URL
    "org.opencontainers.image.version" = IMAGE_TAG
  }
}

target "neuroforge" {
  inherits = ["_common"]
  context = "./platform/neuroforge"
  dockerfile = "Dockerfile"
  target = "server"
  tags = tags("neuroforge")
}

target "neuroforge-worker" {
  inherits = ["_common"]
  context = "./platform/neuroforge"
  dockerfile = "Dockerfile"
  target = "worker"
  tags = tags("neuroforge-worker")
}

target "agent" {
  inherits = ["_common"]
  context = "./services/agent"
  dockerfile = "Dockerfile"
  tags = tags("agent")
}

target "agent-data-init" {
  inherits = ["_common"]
  context = "./services/agent"
  dockerfile = "Dockerfile"
  target = "data-init"
  tags = tags("agent-data-init")
}

target "knowledge" {
  inherits = ["_common"]
  context = "./services/knowledge"
  dockerfile = "Dockerfile"
  tags = tags("knowledge")
}

target "control" {
  inherits = ["_common"]
  context = "./services/control"
  dockerfile = "Dockerfile"
  tags = tags("control")
}
