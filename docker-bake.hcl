variable "IMAGE_TAG" {
  default = "1.6.2"
}

variable "REGISTRY" {
  default = "git.send.nrw/sendnrw"
}

group "default" {
  targets = ["neuroforge", "neuroforge-worker", "agent", "agent-data-init", "knowledge", "control"]
}

target "neuroforge" {
  context = "./platform/neuroforge"
  dockerfile = "Dockerfile"
  target = "server"
  tags = ["${REGISTRY}/glpi-neuroforge-mega-neuroforge:${IMAGE_TAG}"]
  platforms = ["linux/amd64"]
}

target "neuroforge-worker" {
  context = "./platform/neuroforge"
  dockerfile = "Dockerfile"
  target = "worker"
  tags = ["${REGISTRY}/glpi-neuroforge-mega-neuroforge-worker:${IMAGE_TAG}"]
  platforms = ["linux/amd64"]
}

target "agent" {
  context = "./services/agent"
  dockerfile = "Dockerfile"
  tags = ["${REGISTRY}/glpi-neuroforge-mega-agent:${IMAGE_TAG}"]
  platforms = ["linux/amd64"]
}

target "agent-data-init" {
  context = "./services/agent"
  dockerfile = "Dockerfile"
  target = "data-init"
  tags = ["${REGISTRY}/glpi-neuroforge-mega-agent-data-init:${IMAGE_TAG}"]
  platforms = ["linux/amd64"]
}

target "knowledge" {
  context = "./services/knowledge"
  dockerfile = "Dockerfile"
  tags = ["${REGISTRY}/glpi-neuroforge-mega-knowledge:${IMAGE_TAG}"]
  platforms = ["linux/amd64"]
}

target "control" {
  context = "./services/control"
  dockerfile = "Dockerfile"
  tags = ["${REGISTRY}/glpi-neuroforge-mega-control:${IMAGE_TAG}"]
  platforms = ["linux/amd64"]
}
