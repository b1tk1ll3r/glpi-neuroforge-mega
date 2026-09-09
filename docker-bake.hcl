variable "IMAGE_TAG" {
  default = "1.6.2"
}

variable "REGISTRY" {
  default = "git.send.nrw/sendnrw"
}

group "default" {
  targets = [
    "neuroforge",
    "neuroforge-worker",
    "agent",
    "agent-data-init",
    "knowledge",
    "control",
  ]
}

target "neuroforge" {
  context    = "./platform/neuroforge"
  dockerfile = "Dockerfile"
  target     = "server"
  tags       = ["${REGISTRY}/glpi-neuroforge-mega-neuroforge:${IMAGE_TAG}"]
}

target "neuroforge-worker" {
  context    = "./platform/neuroforge"
  dockerfile = "Dockerfile"
  target     = "worker"
  tags       = ["${REGISTRY}/glpi-neuroforge-mega-neuroforge-worker:${IMAGE_TAG}"]
}

target "agent" {
  context    = "./services/agent"
  dockerfile = "Dockerfile"
  tags       = ["${REGISTRY}/glpi-neuroforge-mega-agent:${IMAGE_TAG}"]
}

target "agent-data-init" {
  context    = "./services/agent"
  dockerfile = "Dockerfile"
  target     = "data-init"
  tags       = ["${REGISTRY}/glpi-neuroforge-mega-agent-data-init:${IMAGE_TAG}"]
}

target "knowledge" {
  context    = "./services/knowledge"
  dockerfile = "Dockerfile"
  tags       = ["${REGISTRY}/glpi-neuroforge-mega-knowledge:${IMAGE_TAG}"]
}

target "control" {
  context    = "./services/control"
  dockerfile = "Dockerfile"
  tags       = ["${REGISTRY}/glpi-neuroforge-mega-control:${IMAGE_TAG}"]
}
