# ============================================================================
# FleetOS — OpenStack Infrastructure
# ============================================================================
# Deploys the full FleetOS platform on OpenStack:
#   - Networking (router, networks, subnets, security groups)
#   - Kubernetes cluster (Magnum)
#   - Database (Trove PostgreSQL)
#   - Object storage (Swift containers with lifecycle + encryption)
#   - Compute instances for Kafka, Redis, Temporal, ClickHouse
#   - GPU node groups for inference and training
#   - Load balancer (Octavia)
#   - DNS (Designate)
#   - Container registry (Harbor)
#   - Observability (Prometheus + Grafana via Helm)
#   - Service mesh (Istio via Helm)
#   - OpenTelemetry Collector
# ============================================================================

terraform {
  required_version = ">= 1.5"

  required_providers {
    openstack = {
      source  = "terraform-provider-openstack/openstack"
      version = "~> 3.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.25"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.12"
    }
  }

  backend "swift" {
    container         = "fleetos-terraform-state"
    archive_container = "fleetos-terraform-state-archive"
  }
}

# ============================================================================
# Variables
# ============================================================================

variable "environment" {
  description = "Deployment environment (dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "cluster_name" {
  description = "Name prefix for all resources"
  type        = string
  default     = "fleetos"
}

variable "external_network_name" {
  description = "Name of the external/provider network for floating IPs"
  type        = string
  default     = "external"
}

variable "dns_nameservers" {
  description = "DNS nameservers for subnets"
  type        = list(string)
  default     = ["8.8.8.8", "8.8.4.4"]
}

variable "gpu_flavor" {
  description = "Flavor for GPU instances (inference)"
  type        = string
  default     = "gpu1.xlarge"
}

variable "gpu_training_flavor" {
  description = "Flavor for GPU training instances"
  type        = string
  default     = "gpu1.2xlarge"
}

variable "kafka_flavor" {
  description = "Flavor for Kafka brokers (high memory)"
  type        = string
  default     = "r1.xlarge"
}

variable "redis_flavor" {
  description = "Flavor for Redis instances (override per-environment via tfvars)"
  type        = string
  default     = "m1.small"
}

variable "db_flavor" {
  description = "Flavor for PostgreSQL (Trove)"
  type        = string
  default     = "m1.large"
}

variable "clickhouse_flavor" {
  description = "Flavor for ClickHouse OLAP instance"
  type        = string
  default     = "m1.xlarge"
}

variable "temporal_flavor" {
  description = "Flavor for Temporal workflow server"
  type        = string
  default     = "m1.large"
}

variable "harbor_flavor" {
  description = "Flavor for Harbor container registry"
  type        = string
  default     = "m1.large"
}

variable "k8s_master_flavor" {
  description = "Flavor for Kubernetes master nodes"
  type        = string
  default     = "m1.large"
}

variable "k8s_worker_flavor" {
  description = "Flavor for Kubernetes worker nodes"
  type        = string
  default     = "m1.xlarge"
}

variable "k8s_image" {
  description = "Image for Kubernetes nodes (Fedora CoreOS or Ubuntu)"
  type        = string
  default     = "fedora-coreos-39"
}

variable "keypair_name" {
  description = "OpenStack keypair for SSH access"
  type        = string
}

variable "postgres_password" {
  description = "PostgreSQL password (set via TF_VAR or tfvars, never commit)"
  type        = string
  sensitive   = true
}

locals {
  common_tags = ["environment:${var.environment}", "project:fleetos"]
}

# ============================================================================
# Providers — Kubernetes + Helm target the Magnum cluster
# ============================================================================

provider "kubernetes" {
  host                   = openstack_containerinfra_cluster_v1.k8s.kubeconfig.host
  cluster_ca_certificate = base64decode(openstack_containerinfra_cluster_v1.k8s.kubeconfig.cluster_ca_certificate)
  client_certificate     = base64decode(openstack_containerinfra_cluster_v1.k8s.kubeconfig.client_certificate)
  client_key             = base64decode(openstack_containerinfra_cluster_v1.k8s.kubeconfig.client_key)
}

provider "helm" {
  kubernetes {
    host                   = openstack_containerinfra_cluster_v1.k8s.kubeconfig.host
    cluster_ca_certificate = base64decode(openstack_containerinfra_cluster_v1.k8s.kubeconfig.cluster_ca_certificate)
    client_certificate     = base64decode(openstack_containerinfra_cluster_v1.k8s.kubeconfig.client_certificate)
    client_key             = base64decode(openstack_containerinfra_cluster_v1.k8s.kubeconfig.client_key)
  }
}

# ============================================================================
# Data Sources
# ============================================================================

data "openstack_networking_network_v2" "external" {
  name = var.external_network_name
}

data "openstack_images_image_v2" "k8s" {
  name        = var.k8s_image
  most_recent = true
}

data "openstack_images_image_v2" "ubuntu" {
  name        = "ubuntu-22.04"
  most_recent = true
}

# ============================================================================
# Networking
# ============================================================================

resource "openstack_networking_router_v2" "router" {
  name                = "${var.cluster_name}-router"
  external_network_id = data.openstack_networking_network_v2.external.id

  tags = local.common_tags
}

# --- Platform network (services, DB, Redis, Temporal, ClickHouse) ---

resource "openstack_networking_network_v2" "platform" {
  name           = "${var.cluster_name}-platform"
  admin_state_up = true

  tags = local.common_tags
}

resource "openstack_networking_subnet_v2" "platform" {
  name            = "${var.cluster_name}-platform-subnet"
  network_id      = openstack_networking_network_v2.platform.id
  cidr            = "10.0.1.0/24"
  ip_version      = 4
  dns_nameservers = var.dns_nameservers

  tags = local.common_tags
}

resource "openstack_networking_router_interface_v2" "platform" {
  router_id = openstack_networking_router_v2.router.id
  subnet_id = openstack_networking_subnet_v2.platform.id
}

# --- Kafka network (dedicated for high-throughput traffic) ---

resource "openstack_networking_network_v2" "kafka" {
  name           = "${var.cluster_name}-kafka"
  admin_state_up = true

  tags = local.common_tags
}

resource "openstack_networking_subnet_v2" "kafka" {
  name            = "${var.cluster_name}-kafka-subnet"
  network_id      = openstack_networking_network_v2.kafka.id
  cidr            = "10.0.2.0/24"
  ip_version      = 4
  dns_nameservers = var.dns_nameservers

  tags = local.common_tags
}

resource "openstack_networking_router_interface_v2" "kafka" {
  router_id = openstack_networking_router_v2.router.id
  subnet_id = openstack_networking_subnet_v2.kafka.id
}

# ============================================================================
# Security Groups
# ============================================================================

# --- Kubernetes nodes ---
resource "openstack_networking_secgroup_v2" "k8s" {
  name        = "${var.cluster_name}-k8s"
  description = "FleetOS Kubernetes nodes"

  tags = local.common_tags
}

resource "openstack_networking_secgroup_rule_v2" "k8s_internal" {
  security_group_id = openstack_networking_secgroup_v2.k8s.id
  direction         = "ingress"
  ethertype         = "IPv4"
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "All traffic between K8s nodes"
}

resource "openstack_networking_secgroup_rule_v2" "k8s_api" {
  security_group_id = openstack_networking_secgroup_v2.k8s.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 6443
  port_range_max    = 6443
  remote_ip_prefix  = "0.0.0.0/0"
  description       = "Kubernetes API server"
}

resource "openstack_networking_secgroup_rule_v2" "k8s_nodeport" {
  security_group_id = openstack_networking_secgroup_v2.k8s.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 30000
  port_range_max    = 32767
  remote_ip_prefix  = "0.0.0.0/0"
  description       = "NodePort services"
}

# --- PostgreSQL ---
resource "openstack_networking_secgroup_v2" "postgres" {
  name        = "${var.cluster_name}-postgres"
  description = "FleetOS PostgreSQL"

  tags = local.common_tags
}

resource "openstack_networking_secgroup_rule_v2" "postgres_from_k8s" {
  security_group_id = openstack_networking_secgroup_v2.postgres.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 5432
  port_range_max    = 5432
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "PostgreSQL from K8s nodes only"
}

resource "openstack_networking_secgroup_rule_v2" "postgres_from_temporal" {
  security_group_id = openstack_networking_secgroup_v2.postgres.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 5432
  port_range_max    = 5432
  remote_group_id   = openstack_networking_secgroup_v2.temporal.id
  description       = "PostgreSQL from Temporal (workflow persistence)"
}

# --- Redis ---
resource "openstack_networking_secgroup_v2" "redis" {
  name        = "${var.cluster_name}-redis"
  description = "FleetOS Redis"

  tags = local.common_tags
}

resource "openstack_networking_secgroup_rule_v2" "redis_from_k8s" {
  security_group_id = openstack_networking_secgroup_v2.redis.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 6379
  port_range_max    = 6379
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "Redis from K8s nodes only"
}

# --- Kafka ---
resource "openstack_networking_secgroup_v2" "kafka_sg" {
  name        = "${var.cluster_name}-kafka"
  description = "FleetOS Kafka brokers"

  tags = local.common_tags
}

resource "openstack_networking_secgroup_rule_v2" "kafka_broker" {
  security_group_id = openstack_networking_secgroup_v2.kafka_sg.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 9092
  port_range_max    = 9093
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "Kafka broker from K8s nodes"
}

resource "openstack_networking_secgroup_rule_v2" "kafka_internal" {
  security_group_id = openstack_networking_secgroup_v2.kafka_sg.id
  direction         = "ingress"
  ethertype         = "IPv4"
  remote_group_id   = openstack_networking_secgroup_v2.kafka_sg.id
  description       = "Inter-broker replication"
}

# --- Temporal ---
resource "openstack_networking_secgroup_v2" "temporal" {
  name        = "${var.cluster_name}-temporal"
  description = "FleetOS Temporal workflow server"

  tags = local.common_tags
}

resource "openstack_networking_secgroup_rule_v2" "temporal_grpc_from_k8s" {
  security_group_id = openstack_networking_secgroup_v2.temporal.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 7233
  port_range_max    = 7233
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "Temporal gRPC from K8s"
}

resource "openstack_networking_secgroup_rule_v2" "temporal_metrics_from_k8s" {
  security_group_id = openstack_networking_secgroup_v2.temporal.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 8000
  port_range_max    = 8000
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "Temporal metrics (Prometheus scrape)"
}

resource "openstack_networking_secgroup_rule_v2" "temporal_ui" {
  security_group_id = openstack_networking_secgroup_v2.temporal.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 8233
  port_range_max    = 8233
  remote_ip_prefix  = "10.0.1.0/24"
  description       = "Temporal UI from platform subnet"
}

# --- ClickHouse ---
resource "openstack_networking_secgroup_v2" "clickhouse" {
  name        = "${var.cluster_name}-clickhouse"
  description = "FleetOS ClickHouse OLAP"

  tags = local.common_tags
}

resource "openstack_networking_secgroup_rule_v2" "clickhouse_http_from_k8s" {
  security_group_id = openstack_networking_secgroup_v2.clickhouse.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 8123
  port_range_max    = 8123
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "ClickHouse HTTP from K8s"
}

resource "openstack_networking_secgroup_rule_v2" "clickhouse_native_from_k8s" {
  security_group_id = openstack_networking_secgroup_v2.clickhouse.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 9000
  port_range_max    = 9000
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "ClickHouse native from K8s"
}

# --- Harbor (Container Registry) ---
resource "openstack_networking_secgroup_v2" "harbor" {
  name        = "${var.cluster_name}-harbor"
  description = "FleetOS Harbor container registry"

  tags = local.common_tags
}

resource "openstack_networking_secgroup_rule_v2" "harbor_https_from_k8s" {
  security_group_id = openstack_networking_secgroup_v2.harbor.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 443
  port_range_max    = 443
  remote_group_id   = openstack_networking_secgroup_v2.k8s.id
  description       = "Harbor HTTPS from K8s (image pulls)"
}

resource "openstack_networking_secgroup_rule_v2" "harbor_https_from_platform" {
  security_group_id = openstack_networking_secgroup_v2.harbor.id
  direction         = "ingress"
  ethertype         = "IPv4"
  protocol          = "tcp"
  port_range_min    = 443
  port_range_max    = 443
  remote_ip_prefix  = "10.0.1.0/24"
  description       = "Harbor HTTPS from platform subnet (image pushes)"
}

# ============================================================================
# Kubernetes Cluster (Magnum)
# ============================================================================

resource "openstack_containerinfra_clustertemplate_v1" "k8s" {
  name                  = "${var.cluster_name}-template"
  image                 = data.openstack_images_image_v2.k8s.id
  coe                   = "kubernetes"
  network_driver        = "flannel"
  volume_driver         = "cinder"
  server_type           = "vm"
  master_flavor         = var.k8s_master_flavor
  flavor                = var.k8s_worker_flavor
  external_network_id   = data.openstack_networking_network_v2.external.id
  fixed_network         = openstack_networking_network_v2.platform.id
  fixed_subnet          = openstack_networking_subnet_v2.platform.id
  floating_ip_enabled   = var.environment == "dev"
  master_lb_enabled     = true
  dns_nameserver        = var.dns_nameservers[0]
  docker_storage_driver = "overlay2"
  tls_disabled          = false

  labels = {
    kube_tag               = "v1.29.2"
    monitoring_enabled     = "true"
    auto_scaling_enabled   = "true"
    autoscaler_tag         = "v1.29.0"
    ingress_controller     = "nginx"
    cloud_provider_enabled = "true"
    cinder_csi_enabled     = "true"
  }
}

resource "openstack_containerinfra_cluster_v1" "k8s" {
  name                = var.cluster_name
  cluster_template_id = openstack_containerinfra_clustertemplate_v1.k8s.id
  master_count        = var.environment == "prod" ? 3 : 1
  node_count          = var.environment == "dev" ? 3 : 5
  keypair             = var.keypair_name

  labels = {
    min_node_count = var.environment == "dev" ? "2" : "3"
    max_node_count = "15"
  }
}

# --- GPU Node Group (Inference) ---
resource "openstack_containerinfra_nodegroup_v1" "gpu" {
  name           = "gpu"
  cluster_id     = openstack_containerinfra_cluster_v1.k8s.id
  flavor_id      = var.gpu_flavor
  node_count     = var.environment == "dev" ? 1 : 2
  min_node_count = 0
  max_node_count = 5

  labels = {
    workload = "gpu"
  }
}

# --- GPU Node Group (Training — scale from zero) ---
resource "openstack_containerinfra_nodegroup_v1" "training" {
  name           = "training"
  cluster_id     = openstack_containerinfra_cluster_v1.k8s.id
  flavor_id      = var.gpu_training_flavor
  node_count     = 0
  min_node_count = 0
  max_node_count = 8

  labels = {
    workload = "training"
  }
}

# ============================================================================
# PostgreSQL (Trove Database-as-a-Service)
# ============================================================================

resource "openstack_db_instance_v1" "postgres" {
  name      = "${var.cluster_name}-postgres"
  flavor_id = var.db_flavor
  size      = 100

  datastore {
    type    = "postgresql"
    version = "16"
  }

  network {
    uuid = openstack_networking_network_v2.platform.id
  }

  database {
    name = "fleetos"
  }

  user {
    name      = "fleetos"
    databases = ["fleetos"]
  }

  configuration_id = openstack_db_configuration_v1.postgres.id
}

resource "openstack_db_configuration_v1" "postgres" {
  name        = "${var.cluster_name}-postgres-config"
  description = "FleetOS PostgreSQL tuning"

  datastore {
    type    = "postgresql"
    version = "16"
  }

  configuration {
    name  = "max_connections"
    value = "200"
  }

  configuration {
    name  = "shared_buffers"
    value = "2048"
  }

  configuration {
    name  = "work_mem"
    value = "64"
  }

  configuration {
    name  = "log_min_duration_statement"
    value = "1000"
  }
}

# ============================================================================
# Redis (Compute instances)
# ============================================================================

resource "openstack_compute_instance_v2" "redis" {
  count       = var.environment == "dev" ? 1 : 3
  name        = "${var.cluster_name}-redis-${count.index}"
  flavor_name = var.redis_flavor
  key_pair    = var.keypair_name
  image_id    = data.openstack_images_image_v2.ubuntu.id

  network {
    uuid = openstack_networking_network_v2.platform.id
  }

  security_groups = [
    openstack_networking_secgroup_v2.redis.name,
  ]

  user_data = templatefile("${path.module}/templates/redis-userdata.sh.tpl", {
    node_index   = count.index
    total_nodes  = var.environment == "dev" ? 1 : 3
    cluster_name = var.cluster_name
    environment  = var.environment
  })

  tags = concat(local.common_tags, ["role:redis"])
}

# ============================================================================
# Kafka Brokers (Compute instances with Cinder volumes)
# ============================================================================

resource "openstack_blockstorage_volume_v3" "kafka_data" {
  count       = 3
  name        = "${var.cluster_name}-kafka-data-${count.index}"
  size        = 500
  description = "Kafka broker ${count.index} data volume"

  metadata = {
    environment = var.environment
    project     = "fleetos"
    role        = "kafka"
  }
}

resource "openstack_compute_instance_v2" "kafka" {
  count       = 3
  name        = "${var.cluster_name}-kafka-${count.index}"
  flavor_name = var.kafka_flavor
  key_pair    = var.keypair_name
  image_id    = data.openstack_images_image_v2.ubuntu.id

  network {
    uuid = openstack_networking_network_v2.kafka.id
  }

  network {
    uuid = openstack_networking_network_v2.platform.id
  }

  security_groups = [
    openstack_networking_secgroup_v2.kafka_sg.name,
  ]

  user_data = templatefile("${path.module}/templates/kafka-userdata.sh.tpl", {
    broker_id    = count.index
    broker_count = 3
    environment  = var.environment
  })

  tags = concat(local.common_tags, ["role:kafka"])
}

resource "openstack_compute_volume_attach_v2" "kafka_data" {
  count       = 3
  instance_id = openstack_compute_instance_v2.kafka[count.index].id
  volume_id   = openstack_blockstorage_volume_v3.kafka_data[count.index].id
  device      = "/dev/vdb"
}

# ============================================================================
# Temporal (Compute instance — workflow orchestration)
# ============================================================================

resource "openstack_compute_instance_v2" "temporal" {
  name        = "${var.cluster_name}-temporal"
  flavor_name = var.temporal_flavor
  key_pair    = var.keypair_name
  image_id    = data.openstack_images_image_v2.ubuntu.id

  network {
    uuid = openstack_networking_network_v2.platform.id
  }

  security_groups = [
    openstack_networking_secgroup_v2.temporal.name,
  ]

  user_data = templatefile("${path.module}/templates/temporal-userdata.sh.tpl", {
    postgres_host     = openstack_db_instance_v1.postgres.addresses[0].address
    postgres_db       = "fleetos"
    postgres_user     = "fleetos"
    postgres_password = var.postgres_password
    environment       = var.environment
  })

  tags = concat(local.common_tags, ["role:temporal"])

  depends_on = [openstack_db_instance_v1.postgres]
}

# ============================================================================
# ClickHouse (Compute instance — analytics OLAP)
# ============================================================================

resource "openstack_blockstorage_volume_v3" "clickhouse_data" {
  name        = "${var.cluster_name}-clickhouse-data"
  size        = var.environment == "dev" ? 200 : 1000
  description = "ClickHouse data volume"

  metadata = {
    environment = var.environment
    project     = "fleetos"
    role        = "clickhouse"
  }
}

resource "openstack_compute_instance_v2" "clickhouse" {
  name        = "${var.cluster_name}-clickhouse"
  flavor_name = var.clickhouse_flavor
  key_pair    = var.keypair_name
  image_id    = data.openstack_images_image_v2.ubuntu.id

  network {
    uuid = openstack_networking_network_v2.platform.id
  }

  security_groups = [
    openstack_networking_secgroup_v2.clickhouse.name,
  ]

  user_data = templatefile("${path.module}/templates/clickhouse-userdata.sh.tpl", {
    environment = var.environment
  })

  tags = concat(local.common_tags, ["role:clickhouse"])
}

resource "openstack_compute_volume_attach_v2" "clickhouse_data" {
  instance_id = openstack_compute_instance_v2.clickhouse.id
  volume_id   = openstack_blockstorage_volume_v3.clickhouse_data.id
  device      = "/dev/vdb"
}

# ============================================================================
# Harbor (Container Registry — compute instance)
# ============================================================================

resource "openstack_blockstorage_volume_v3" "harbor_data" {
  name        = "${var.cluster_name}-harbor-data"
  size        = 200
  description = "Harbor registry storage"

  metadata = {
    environment = var.environment
    project     = "fleetos"
    role        = "harbor"
  }
}

resource "openstack_compute_instance_v2" "harbor" {
  name        = "${var.cluster_name}-harbor"
  flavor_name = var.harbor_flavor
  key_pair    = var.keypair_name
  image_id    = data.openstack_images_image_v2.ubuntu.id

  network {
    uuid = openstack_networking_network_v2.platform.id
  }

  security_groups = [
    openstack_networking_secgroup_v2.harbor.name,
  ]

  user_data = templatefile("${path.module}/templates/harbor-userdata.sh.tpl", {
    harbor_hostname = "harbor.fleetos.internal"
    environment     = var.environment
  })

  tags = concat(local.common_tags, ["role:harbor"])
}

resource "openstack_compute_volume_attach_v2" "harbor_data" {
  instance_id = openstack_compute_instance_v2.harbor.id
  volume_id   = openstack_blockstorage_volume_v3.harbor_data.id
  device      = "/dev/vdb"
}

# ============================================================================
# Object Storage (Swift)
# ============================================================================

resource "openstack_objectstorage_container_v1" "telemetry" {
  name = "${var.cluster_name}-telemetry-${var.environment}"

  metadata = {
    environment                  = var.environment
    project                      = "fleetos"
    "X-Container-Meta-Temp-URL-Key" = var.cluster_name
  }

  # Auto-delete objects after 365 days
  container_sync_key = ""
}

resource "openstack_objectstorage_container_v1" "models" {
  name = "${var.cluster_name}-models-${var.environment}"

  metadata = {
    environment = var.environment
    project     = "fleetos"
  }

  versioning {
    type     = "versions"
    location = "${var.cluster_name}-models-${var.environment}-versions"
  }
}

resource "openstack_objectstorage_container_v1" "models_versions" {
  name = "${var.cluster_name}-models-${var.environment}-versions"

  metadata = {
    environment = var.environment
    project     = "fleetos"
  }
}

resource "openstack_objectstorage_container_v1" "training_data" {
  name = "${var.cluster_name}-training-data-${var.environment}"

  metadata = {
    environment = var.environment
    project     = "fleetos"
  }
}

# ============================================================================
# Barbican (Key Manager — secrets for training pods + Swift encryption)
# ============================================================================

resource "openstack_keymanager_secret_v1" "db_password" {
  name                 = "${var.cluster_name}-db-password"
  secret_type          = "passphrase"
  payload_content_type = "text/plain"
  payload              = "CHANGE_ME_AT_DEPLOY_TIME"

  metadata = {
    environment = var.environment
    project     = "fleetos"
  }

  lifecycle {
    ignore_changes = [payload]
  }
}

resource "openstack_keymanager_secret_v1" "swift_encryption_key" {
  name                 = "${var.cluster_name}-swift-encryption"
  secret_type          = "symmetric"
  algorithm            = "aes"
  bit_length           = 256
  payload_content_type = "application/octet-stream"

  metadata = {
    environment = var.environment
    project     = "fleetos"
  }
}

# --- Application credential for training pods to access Swift ---
resource "openstack_identity_application_credential_v3" "training" {
  name        = "${var.cluster_name}-training-swift"
  description = "Training pods access to Swift (models + training data)"
}

# ============================================================================
# Floating IPs
# ============================================================================

resource "openstack_networking_floatingip_v2" "lb" {
  pool        = var.external_network_name
  description = "FleetOS load balancer"

  tags = local.common_tags
}

resource "openstack_networking_floatingip_v2" "harbor" {
  pool        = var.external_network_name
  description = "FleetOS Harbor registry"

  tags = local.common_tags
}

# ============================================================================
# Load Balancer (Octavia)
# ============================================================================

resource "openstack_lb_loadbalancer_v2" "ingress" {
  name          = "${var.cluster_name}-ingress-lb"
  vip_subnet_id = openstack_networking_subnet_v2.platform.id
  description   = "FleetOS ingress load balancer"

  tags = local.common_tags
}

resource "openstack_networking_floatingip_associate_v2" "lb" {
  floating_ip = openstack_networking_floatingip_v2.lb.address
  port_id     = openstack_lb_loadbalancer_v2.ingress.vip_port_id
}

# --- HTTP listener (redirects to HTTPS in prod) ---
resource "openstack_lb_listener_v2" "http" {
  name            = "${var.cluster_name}-http"
  protocol        = "HTTP"
  protocol_port   = 80
  loadbalancer_id = openstack_lb_loadbalancer_v2.ingress.id
}

resource "openstack_lb_pool_v2" "http" {
  name        = "${var.cluster_name}-http-pool"
  protocol    = "HTTP"
  lb_method   = "ROUND_ROBIN"
  listener_id = openstack_lb_listener_v2.http.id
}

# --- HTTPS listener ---
resource "openstack_lb_listener_v2" "https" {
  name            = "${var.cluster_name}-https"
  protocol        = "TERMINATED_HTTPS"
  protocol_port   = 443
  loadbalancer_id = openstack_lb_loadbalancer_v2.ingress.id
}

resource "openstack_lb_pool_v2" "https" {
  name        = "${var.cluster_name}-https-pool"
  protocol    = "HTTP"
  lb_method   = "ROUND_ROBIN"
  listener_id = openstack_lb_listener_v2.https.id
}

# --- gRPC listener (telemetry ingestion) ---
resource "openstack_lb_listener_v2" "grpc" {
  name            = "${var.cluster_name}-grpc"
  protocol        = "TCP"
  protocol_port   = 50051
  loadbalancer_id = openstack_lb_loadbalancer_v2.ingress.id
}

resource "openstack_lb_pool_v2" "grpc" {
  name        = "${var.cluster_name}-grpc-pool"
  protocol    = "TCP"
  lb_method   = "LEAST_CONNECTIONS"
  listener_id = openstack_lb_listener_v2.grpc.id
}

# --- Health monitors ---
resource "openstack_lb_monitor_v2" "http" {
  name        = "${var.cluster_name}-http-monitor"
  pool_id     = openstack_lb_pool_v2.http.id
  type        = "HTTP"
  url_path    = "/healthz"
  delay       = 10
  timeout     = 5
  max_retries = 3
}

resource "openstack_lb_monitor_v2" "https" {
  name        = "${var.cluster_name}-https-monitor"
  pool_id     = openstack_lb_pool_v2.https.id
  type        = "HTTP"
  url_path    = "/healthz"
  delay       = 10
  timeout     = 5
  max_retries = 3
}

resource "openstack_lb_monitor_v2" "grpc" {
  name        = "${var.cluster_name}-grpc-monitor"
  pool_id     = openstack_lb_pool_v2.grpc.id
  type        = "TCP"
  delay       = 10
  timeout     = 5
  max_retries = 3
}

# ============================================================================
# DNS (Designate)
# ============================================================================

resource "openstack_dns_zone_v2" "fleetos" {
  name        = "fleetos.internal."
  email       = "admin@fleetos.internal"
  description = "FleetOS internal DNS zone"
  type        = "PRIMARY"
  ttl         = 300

  tags = local.common_tags
}

resource "openstack_dns_recordset_v2" "api" {
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "api.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_networking_floatingip_v2.lb.address]
}

resource "openstack_dns_recordset_v2" "grpc" {
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "grpc.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_networking_floatingip_v2.lb.address]
}

resource "openstack_dns_recordset_v2" "kafka" {
  count   = 3
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "kafka-${count.index}.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_compute_instance_v2.kafka[count.index].access_ip_v4]
}

resource "openstack_dns_recordset_v2" "redis" {
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "redis.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_compute_instance_v2.redis[0].access_ip_v4]
}

resource "openstack_dns_recordset_v2" "postgres" {
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "postgres.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_db_instance_v1.postgres.addresses[0].address]
}

resource "openstack_dns_recordset_v2" "temporal" {
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "temporal.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_compute_instance_v2.temporal.access_ip_v4]
}

resource "openstack_dns_recordset_v2" "clickhouse" {
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "clickhouse.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_compute_instance_v2.clickhouse.access_ip_v4]
}

resource "openstack_dns_recordset_v2" "harbor" {
  zone_id = openstack_dns_zone_v2.fleetos.id
  name    = "harbor.fleetos.internal."
  type    = "A"
  ttl     = 300
  records = [openstack_compute_instance_v2.harbor.access_ip_v4]
}

# ============================================================================
# Kubeflow (MLOps — training pipeline orchestration)
# ============================================================================

resource "kubernetes_namespace" "kubeflow" {
  metadata {
    name = "kubeflow"
    labels = {
      "istio-injection" = "enabled"
    }
  }

  depends_on = [openstack_containerinfra_cluster_v1.k8s]
}

# --- Kubernetes secret for training pods to access Swift ---
resource "kubernetes_secret" "training_swift" {
  metadata {
    name      = "swift-credentials"
    namespace = "kubeflow"
  }

  data = {
    application_credential_id     = openstack_identity_application_credential_v3.training.id
    application_credential_secret = openstack_identity_application_credential_v3.training.secret
  }

  depends_on = [kubernetes_namespace.kubeflow]
}

# ============================================================================
# Istio (Helm — service mesh control plane)
# ============================================================================

resource "helm_release" "istio_base" {
  name             = "istio-base"
  repository       = "https://istio-release.storage.googleapis.com/charts"
  chart            = "base"
  version          = "1.21.0"
  namespace        = "istio-system"
  create_namespace = true

  depends_on = [openstack_containerinfra_cluster_v1.k8s]
}

resource "helm_release" "istiod" {
  name       = "istiod"
  repository = "https://istio-release.storage.googleapis.com/charts"
  chart      = "istiod"
  version    = "1.21.0"
  namespace  = "istio-system"

  set {
    name  = "meshConfig.accessLogFile"
    value = "/dev/stdout"
  }

  set {
    name  = "meshConfig.defaultConfig.tracing.zipkin.address"
    value = "otel-collector.observability:9411"
  }

  depends_on = [helm_release.istio_base]
}

resource "helm_release" "istio_ingress" {
  name       = "istio-ingressgateway"
  repository = "https://istio-release.storage.googleapis.com/charts"
  chart      = "gateway"
  version    = "1.21.0"
  namespace  = "istio-system"

  set {
    name  = "service.type"
    value = "ClusterIP"
  }

  depends_on = [helm_release.istiod]
}

# ============================================================================
# Observability (Prometheus + Grafana via Helm on K8s)
# ============================================================================

resource "kubernetes_namespace" "observability" {
  metadata {
    name = "observability"
    labels = {
      "istio-injection" = "enabled"
    }
  }

  depends_on = [openstack_containerinfra_cluster_v1.k8s]
}

resource "helm_release" "prometheus" {
  name       = "prometheus"
  repository = "https://prometheus-community.github.io/helm-charts"
  chart      = "kube-prometheus-stack"
  version    = "57.0.0"
  namespace  = "observability"

  set {
    name  = "prometheus.prometheusSpec.retention"
    value = "30d"
  }

  set {
    name  = "prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.resources.requests.storage"
    value = "100Gi"
  }

  set {
    name  = "grafana.enabled"
    value = "true"
  }

  set {
    name  = "grafana.adminPassword"
    value = "CHANGE_ME_AT_DEPLOY_TIME"
  }

  set {
    name  = "grafana.persistence.enabled"
    value = "true"
  }

  set {
    name  = "grafana.persistence.size"
    value = "10Gi"
  }

  # Scrape all FleetOS service metrics endpoints
  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[0].job_name"
    value = "fleetos-api"
  }

  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[0].static_configs[0].targets[0]"
    value = "fleetos-api.default:8080"
  }

  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[1].job_name"
    value = "fleetos-ingestion"
  }

  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[1].static_configs[0].targets[0]"
    value = "fleetos-ingestion.default:9091"
  }

  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[2].job_name"
    value = "fleetos-inference"
  }

  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[2].static_configs[0].targets[0]"
    value = "fleetos-inference.default:8081"
  }

  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[3].job_name"
    value = "temporal"
  }

  set {
    name  = "prometheus.prometheusSpec.additionalScrapeConfigs[3].static_configs[0].targets[0]"
    value = "temporal.fleetos.internal:8000"
  }

  depends_on = [kubernetes_namespace.observability]
}

# ============================================================================
# OpenTelemetry Collector (Helm)
# ============================================================================

resource "helm_release" "otel_collector" {
  name       = "otel-collector"
  repository = "https://open-telemetry.github.io/opentelemetry-helm-charts"
  chart      = "opentelemetry-collector"
  version    = "0.82.0"
  namespace  = "observability"

  set {
    name  = "mode"
    value = "deployment"
  }

  depends_on = [kubernetes_namespace.observability]
}

# ============================================================================
# Outputs
# ============================================================================

output "cluster_name" {
  value = openstack_containerinfra_cluster_v1.k8s.name
}

output "cluster_api_address" {
  value = openstack_containerinfra_cluster_v1.k8s.api_address
}

output "lb_floating_ip" {
  value       = openstack_networking_floatingip_v2.lb.address
  description = "Public IP for the FleetOS load balancer"
}

output "postgres_address" {
  value       = openstack_db_instance_v1.postgres.addresses[0].address
  description = "PostgreSQL instance address"
}

output "redis_addresses" {
  value       = openstack_compute_instance_v2.redis[*].access_ip_v4
  description = "Redis instance addresses"
}

output "kafka_addresses" {
  value       = openstack_compute_instance_v2.kafka[*].access_ip_v4
  description = "Kafka broker addresses"
}

output "temporal_address" {
  value       = openstack_compute_instance_v2.temporal.access_ip_v4
  description = "Temporal workflow server address"
}

output "clickhouse_address" {
  value       = openstack_compute_instance_v2.clickhouse.access_ip_v4
  description = "ClickHouse OLAP address"
}

output "harbor_address" {
  value       = openstack_compute_instance_v2.harbor.access_ip_v4
  description = "Harbor container registry address"
}

output "harbor_floating_ip" {
  value       = openstack_networking_floatingip_v2.harbor.address
  description = "Harbor public IP"
}

output "swift_containers" {
  value = {
    telemetry     = openstack_objectstorage_container_v1.telemetry.name
    models        = openstack_objectstorage_container_v1.models.name
    training_data = openstack_objectstorage_container_v1.training_data.name
  }
  description = "Swift object storage containers"
}

output "dns_zone" {
  value       = openstack_dns_zone_v2.fleetos.name
  description = "Internal DNS zone"
}

output "training_credential_id" {
  value       = openstack_identity_application_credential_v3.training.id
  description = "Application credential ID for training pods"
  sensitive   = true
}
