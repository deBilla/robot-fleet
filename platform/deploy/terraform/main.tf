terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
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

  backend "s3" {
    bucket = "fleetos-terraform-state"
    key    = "infrastructure/terraform.tfstate"
    region = "us-west-2"
  }
}

provider "aws" {
  region = var.aws_region
}

data "aws_eks_cluster_auth" "cluster" {
  name = module.eks.cluster_name

  depends_on = [module.eks]
}

provider "kubernetes" {
  host                   = module.eks.cluster_endpoint
  cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
  token                  = data.aws_eks_cluster_auth.cluster.token
}

provider "helm" {
  kubernetes {
    host                   = module.eks.cluster_endpoint
    cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
    token                  = data.aws_eks_cluster_auth.cluster.token
  }
}

variable "aws_region" {
  default = "us-west-2"
}

variable "environment" {
  default = "dev"
}

variable "cluster_name" {
  default = "fleetos"
}

variable "domain_name" {
  description = "Base domain for DNS records"
  type        = string
  default     = "fleetos.dev"
}

locals {
  common_tags = {
    Environment = var.environment
    Project     = "fleetos"
  }
}

# ============================================================================
# VPC
# ============================================================================

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.5.1"

  name = "${var.cluster_name}-vpc"
  cidr = "10.0.0.0/16"

  azs             = ["${var.aws_region}a", "${var.aws_region}b", "${var.aws_region}c"]
  private_subnets = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
  public_subnets  = ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24"]

  enable_nat_gateway = true
  single_nat_gateway = var.environment == "dev"

  tags = local.common_tags
}

# ============================================================================
# EKS Cluster
# ============================================================================

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "20.2.1"

  cluster_name    = var.cluster_name
  cluster_version = "1.29"

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  cluster_endpoint_public_access  = var.environment == "dev"
  cluster_endpoint_private_access = true

  cluster_enabled_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  eks_managed_node_groups = {
    general = {
      instance_types = ["m6i.xlarge"]
      min_size       = 2
      max_size       = 10
      desired_size   = 3

      labels = {
        workload = "general"
      }
    }

    gpu = {
      instance_types = ["g5.xlarge"]
      min_size       = 0
      max_size       = 5
      desired_size   = 1
      ami_type       = "AL2_x86_64_GPU"

      labels = {
        workload = "gpu"
      }

      taints = [{
        key    = "nvidia.com/gpu"
        value  = "true"
        effect = "NO_SCHEDULE"
      }]
    }

    training = {
      instance_types = ["g5.2xlarge"]
      min_size       = 0
      max_size       = 8
      desired_size   = 0
      ami_type       = "AL2_x86_64_GPU"

      labels = {
        workload = "training"
      }

      taints = [{
        key    = "nvidia.com/gpu"
        value  = "true"
        effect = "NO_SCHEDULE"
      }]
    }

    kafka = {
      instance_types = ["r6i.xlarge"]
      min_size       = 3
      max_size       = 6
      desired_size   = 3

      labels = {
        workload = "kafka"
      }
    }
  }

  tags = local.common_tags
}

# ============================================================================
# MSK (Managed Kafka)
# ============================================================================

resource "aws_msk_cluster" "kafka" {
  cluster_name           = "${var.cluster_name}-kafka"
  kafka_version          = "3.7.x"
  number_of_broker_nodes = 3

  broker_node_group_info {
    instance_type   = var.environment == "dev" ? "kafka.m5.large" : "kafka.m5.2xlarge"
    client_subnets  = module.vpc.private_subnets
    security_groups = [aws_security_group.kafka.id]

    storage_info {
      ebs_storage_info {
        volume_size = 500
      }
    }
  }

  encryption_info {
    encryption_in_transit {
      client_broker = "TLS_PLAINTEXT"
      in_cluster    = true
    }
  }

  configuration_info {
    arn      = aws_msk_configuration.kafka.arn
    revision = aws_msk_configuration.kafka.latest_revision
  }

  open_monitoring {
    prometheus {
      jmx_exporter {
        enabled_in_broker = true
      }
      node_exporter {
        enabled_in_broker = true
      }
    }
  }

  logging_info {
    broker_logs {
      cloudwatch_logs {
        enabled   = true
        log_group = aws_cloudwatch_log_group.kafka.name
      }
    }
  }

  tags = local.common_tags
}

resource "aws_msk_configuration" "kafka" {
  name              = "${var.cluster_name}-kafka-config"
  kafka_versions    = ["3.7.x"]
  server_properties = <<-PROPERTIES
    auto.create.topics.enable=false
    num.partitions=12
    default.replication.factor=3
    min.insync.replicas=2
    log.retention.hours=168
    log.segment.bytes=1073741824
  PROPERTIES
}

resource "aws_cloudwatch_log_group" "kafka" {
  name              = "/aws/msk/${var.cluster_name}"
  retention_in_days = var.environment == "prod" ? 90 : 14

  tags = local.common_tags
}

resource "aws_security_group" "kafka" {
  name_prefix = "${var.cluster_name}-kafka-"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port       = 9092
    to_port         = 9098
    protocol        = "tcp"
    security_groups = [module.eks.cluster_security_group_id]
    description     = "Kafka brokers from EKS"
  }

  ingress {
    from_port = 9092
    to_port   = 9098
    protocol  = "tcp"
    self      = true
    description = "Inter-broker replication"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.common_tags
}

# ============================================================================
# RDS PostgreSQL
# ============================================================================

module "rds" {
  source  = "terraform-aws-modules/rds/aws"
  version = "6.4.0"

  identifier = "${var.cluster_name}-postgres"

  engine         = "postgres"
  engine_version = "16.1"
  instance_class = var.environment == "dev" ? "db.t4g.medium" : "db.r6g.xlarge"

  allocated_storage     = 100
  max_allocated_storage = 1000

  db_name  = "fleetos"
  username = "fleetos"
  port     = 5432

  manage_master_user_password = true

  multi_az = var.environment != "dev"

  storage_encrypted      = true
  vpc_security_group_ids = [aws_security_group.rds.id]
  subnet_ids             = module.vpc.private_subnets

  family               = "postgres16"
  major_engine_version = "16"

  backup_retention_period  = var.environment == "prod" ? 30 : 7
  backup_window            = "03:00-04:00"
  maintenance_window       = "Mon:04:00-Mon:05:00"
  deletion_protection      = var.environment == "prod"

  monitoring_interval             = var.environment == "prod" ? 60 : 0
  enabled_cloudwatch_logs_exports = ["postgresql", "upgrade"]

  tags = local.common_tags
}

resource "aws_security_group" "rds" {
  name_prefix = "${var.cluster_name}-rds-"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.eks.cluster_security_group_id]
    description     = "PostgreSQL from EKS only"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.common_tags
}

# ============================================================================
# ElastiCache Redis
# ============================================================================

resource "aws_elasticache_replication_group" "redis" {
  replication_group_id = "${var.cluster_name}-redis"
  description          = "FleetOS Redis cluster"

  node_type          = var.environment == "dev" ? "cache.t4g.small" : "cache.r6g.large"
  num_cache_clusters = var.environment == "dev" ? 1 : 3

  engine_version = "7.0"
  port           = 6379

  subnet_group_name  = aws_elasticache_subnet_group.redis.name
  security_group_ids = [module.eks.cluster_security_group_id]

  automatic_failover_enabled = var.environment != "dev"
  at_rest_encryption_enabled = true
  transit_encryption_enabled = var.environment != "dev"

  tags = local.common_tags
}

resource "aws_elasticache_subnet_group" "redis" {
  name       = "${var.cluster_name}-redis"
  subnet_ids = module.vpc.private_subnets
}

# ============================================================================
# S3 Buckets
# ============================================================================

locals {
  s3_buckets = {
    telemetry     = "${var.cluster_name}-telemetry-${var.environment}"
    models        = "${var.cluster_name}-models-${var.environment}"
    training_data = "${var.cluster_name}-training-data-${var.environment}"
  }
}

resource "aws_s3_bucket" "telemetry" {
  bucket = local.s3_buckets["telemetry"]
  tags   = local.common_tags
}

resource "aws_s3_bucket" "models" {
  bucket = local.s3_buckets["models"]
  tags   = local.common_tags
}

resource "aws_s3_bucket" "training_data" {
  bucket = local.s3_buckets["training_data"]
  tags   = local.common_tags
}

resource "aws_s3_bucket_server_side_encryption_configuration" "all" {
  for_each = local.s3_buckets
  bucket   = each.value

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
    bucket_key_enabled = true
  }

  depends_on = [aws_s3_bucket.telemetry, aws_s3_bucket.models, aws_s3_bucket.training_data]
}

resource "aws_s3_bucket_public_access_block" "all" {
  for_each = local.s3_buckets
  bucket   = each.value

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true

  depends_on = [aws_s3_bucket.telemetry, aws_s3_bucket.models, aws_s3_bucket.training_data]
}

resource "aws_s3_bucket_versioning" "models" {
  bucket = aws_s3_bucket.models.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "telemetry" {
  bucket = aws_s3_bucket.telemetry.id

  rule {
    id     = "archive-old-telemetry"
    status = "Enabled"

    transition {
      days          = 90
      storage_class = "GLACIER"
    }

    expiration {
      days = 365
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "training_data" {
  bucket = aws_s3_bucket.training_data.id

  rule {
    id     = "archive-old-experience"
    status = "Enabled"

    filter {
      prefix = "experience/"
    }

    transition {
      days          = 30
      storage_class = "GLACIER"
    }

    expiration {
      days = 180
    }
  }
}

# ============================================================================
# ECR (Container Registry)
# ============================================================================

locals {
  ecr_repos = ["api", "ingestion", "processor", "worker", "inference", "training"]
}

resource "aws_ecr_repository" "services" {
  for_each = toset(local.ecr_repos)

  name                 = "${var.cluster_name}/${each.key}"
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }

  tags = local.common_tags
}

resource "aws_ecr_lifecycle_policy" "services" {
  for_each   = toset(local.ecr_repos)
  repository = aws_ecr_repository.services[each.key].name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep last 30 images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 30
      }
      action = {
        type = "expire"
      }
    }]
  })
}

# ============================================================================
# ALB (API + WebSocket)
# ============================================================================

resource "aws_lb" "api" {
  name               = "${var.cluster_name}-api"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = module.vpc.public_subnets

  tags = local.common_tags
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.api.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = aws_acm_certificate.api.arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}

resource "aws_lb_listener" "http_redirect" {
  load_balancer_arn = aws_lb.api.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_target_group" "api" {
  name        = "${var.cluster_name}-api"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = module.vpc.vpc_id
  target_type = "ip"

  health_check {
    path                = "/healthz"
    port                = "traffic-port"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 15
    timeout             = 5
  }

  tags = local.common_tags
}

resource "aws_security_group" "alb" {
  name_prefix = "${var.cluster_name}-alb-"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "HTTP"
  }

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "HTTPS"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.common_tags
}

# ============================================================================
# NLB (gRPC — telemetry ingestion)
# ============================================================================

resource "aws_lb" "grpc" {
  name               = "${var.cluster_name}-grpc"
  internal           = false
  load_balancer_type = "network"
  subnets            = module.vpc.public_subnets

  tags = local.common_tags
}

resource "aws_lb_listener" "grpc" {
  load_balancer_arn = aws_lb.grpc.arn
  port              = 50051
  protocol          = "TCP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.grpc.arn
  }
}

resource "aws_lb_target_group" "grpc" {
  name        = "${var.cluster_name}-grpc"
  port        = 50051
  protocol    = "TCP"
  vpc_id      = module.vpc.vpc_id
  target_type = "ip"

  health_check {
    protocol            = "TCP"
    port                = "traffic-port"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 10
  }

  tags = local.common_tags
}

# ============================================================================
# Route53 DNS
# ============================================================================

resource "aws_route53_zone" "main" {
  name = var.domain_name

  tags = local.common_tags
}

resource "aws_route53_record" "api" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "api.${var.domain_name}"
  type    = "A"

  alias {
    name                   = aws_lb.api.dns_name
    zone_id                = aws_lb.api.zone_id
    evaluate_target_health = true
  }
}

resource "aws_route53_record" "grpc" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "grpc.${var.domain_name}"
  type    = "A"

  alias {
    name                   = aws_lb.grpc.dns_name
    zone_id                = aws_lb.grpc.zone_id
    evaluate_target_health = true
  }
}

resource "aws_route53_record" "grafana" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "grafana.${var.domain_name}"
  type    = "A"

  alias {
    name                   = aws_lb.api.dns_name
    zone_id                = aws_lb.api.zone_id
    evaluate_target_health = true
  }
}

resource "aws_route53_record" "temporal" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "temporal.${var.domain_name}"
  type    = "A"

  alias {
    name                   = aws_lb.api.dns_name
    zone_id                = aws_lb.api.zone_id
    evaluate_target_health = true
  }
}

resource "aws_acm_certificate" "api" {
  domain_name               = "*.${var.domain_name}"
  subject_alternative_names = [var.domain_name]
  validation_method         = "DNS"

  lifecycle {
    create_before_destroy = true
  }

  tags = local.common_tags
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.api.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      type   = dvo.resource_record_type
      record = dvo.resource_record_value
    }
  }

  zone_id = aws_route53_zone.main.zone_id
  name    = each.value.name
  type    = each.value.type
  ttl     = 300
  records = [each.value.record]
}

resource "aws_acm_certificate_validation" "api" {
  certificate_arn         = aws_acm_certificate.api.arn
  validation_record_fqdns = [for record in aws_route53_record.cert_validation : record.fqdn]
}

# ============================================================================
# Temporal (ECS Fargate — workflow orchestration)
# ============================================================================

resource "aws_ecs_cluster" "temporal" {
  name = "${var.cluster_name}-temporal"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = local.common_tags
}

resource "aws_ecs_task_definition" "temporal" {
  family                   = "${var.cluster_name}-temporal"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.environment == "dev" ? "1024" : "2048"
  memory                   = var.environment == "dev" ? "2048" : "4096"
  execution_role_arn       = aws_iam_role.ecs_execution.arn
  task_role_arn            = aws_iam_role.temporal_task.arn

  container_definitions = jsonencode([
    {
      name  = "temporal"
      image = "temporalio/auto-setup:1.25"
      portMappings = [
        { containerPort = 7233, protocol = "tcp" },
        { containerPort = 8000, protocol = "tcp" },
      ]
      environment = [
        { name = "DB", value = "postgres12" },
        { name = "DB_PORT", value = "5432" },
        { name = "POSTGRES_USER", value = "fleetos" },
        { name = "POSTGRES_DB", value = "fleetos" },
        { name = "POSTGRES_SEEDS", value = module.rds.db_instance_address },
      ]
      secrets = [
        {
          name      = "POSTGRES_PWD"
          valueFrom = module.rds.db_instance_master_user_secret_arn
        },
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.temporal.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "temporal"
        }
      }
    },
    {
      name  = "temporal-ui"
      image = "temporalio/ui:2.31.2"
      portMappings = [
        { containerPort = 8233, protocol = "tcp" },
      ]
      environment = [
        { name = "TEMPORAL_ADDRESS", value = "localhost:7233" },
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.temporal.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "temporal-ui"
        }
      }
    },
  ])

  tags = local.common_tags
}

resource "aws_ecs_service" "temporal" {
  name            = "temporal"
  cluster         = aws_ecs_cluster.temporal.id
  task_definition = aws_ecs_task_definition.temporal.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets         = module.vpc.private_subnets
    security_groups = [aws_security_group.temporal.id]
  }

  service_registries {
    registry_arn = aws_service_discovery_service.temporal.arn
  }

  tags = local.common_tags
}

resource "aws_security_group" "temporal" {
  name_prefix = "${var.cluster_name}-temporal-"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port       = 7233
    to_port         = 7233
    protocol        = "tcp"
    security_groups = [module.eks.cluster_security_group_id]
    description     = "Temporal gRPC from EKS"
  }

  ingress {
    from_port       = 8000
    to_port         = 8000
    protocol        = "tcp"
    security_groups = [module.eks.cluster_security_group_id]
    description     = "Temporal metrics from EKS (Prometheus scrape)"
  }

  ingress {
    from_port       = 8233
    to_port         = 8233
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
    description     = "Temporal UI via ALB"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.common_tags
}

resource "aws_cloudwatch_log_group" "temporal" {
  name              = "/ecs/${var.cluster_name}/temporal"
  retention_in_days = var.environment == "prod" ? 90 : 14

  tags = local.common_tags
}

# --- Temporal service discovery (so EKS pods can find it) ---
resource "aws_service_discovery_private_dns_namespace" "internal" {
  name = "fleetos.internal"
  vpc  = module.vpc.vpc_id

  tags = local.common_tags
}

resource "aws_service_discovery_service" "temporal" {
  name = "temporal"

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.internal.id

    dns_records {
      type = "A"
      ttl  = 10
    }
  }

  health_check_custom_config {
    failure_threshold = 1
  }
}

# --- ECS IAM Roles ---
resource "aws_iam_role" "ecs_execution" {
  name = "${var.cluster_name}-ecs-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy_attachment" "ecs_execution" {
  role       = aws_iam_role.ecs_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role_policy" "ecs_secrets" {
  name = "${var.cluster_name}-ecs-secrets"
  role = aws_iam_role.ecs_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["secretsmanager:GetSecretValue"]
      Resource = [module.rds.db_instance_master_user_secret_arn]
    }]
  })
}

resource "aws_iam_role" "temporal_task" {
  name = "${var.cluster_name}-temporal-task"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })

  tags = local.common_tags
}

# ============================================================================
# ClickHouse (EC2 — analytics OLAP)
# ============================================================================

resource "aws_instance" "clickhouse" {
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.environment == "dev" ? "m6i.large" : "r6i.2xlarge"
  key_name               = "${var.cluster_name}-keypair"
  subnet_id              = module.vpc.private_subnets[0]
  vpc_security_group_ids = [aws_security_group.clickhouse.id]

  root_block_device {
    volume_type = "gp3"
    volume_size = 100
    encrypted   = true
  }

  ebs_block_device {
    device_name = "/dev/xvdb"
    volume_type = "gp3"
    volume_size = var.environment == "dev" ? 200 : 1000
    encrypted   = true
    iops        = 3000
    throughput  = 250
  }

  user_data = <<-EOF
    #!/bin/bash
    set -euo pipefail
    apt-get update -y
    apt-get install -y apt-transport-https ca-certificates curl gnupg
    curl -fsSL https://packages.clickhouse.com/rpm/lts/repodata/repomd.xml.key | gpg --dearmor -o /usr/share/keyrings/clickhouse-keyring.gpg
    echo "deb [signed-by=/usr/share/keyrings/clickhouse-keyring.gpg] https://packages.clickhouse.com/deb stable main" > /etc/apt/sources.list.d/clickhouse.list
    apt-get update -y
    DEBIAN_FRONTEND=noninteractive apt-get install -y clickhouse-server clickhouse-client
    mkfs.ext4 -F /dev/xvdb || true
    mkdir -p /var/lib/clickhouse
    mount /dev/xvdb /var/lib/clickhouse
    echo "/dev/xvdb /var/lib/clickhouse ext4 defaults,nofail 0 2" >> /etc/fstab
    chown -R clickhouse:clickhouse /var/lib/clickhouse
    sed -i 's|<listen_host>::1</listen_host>|<listen_host>0.0.0.0</listen_host>|' /etc/clickhouse-server/config.xml
    systemctl enable clickhouse-server
    systemctl start clickhouse-server
  EOF

  tags = merge(local.common_tags, { Name = "${var.cluster_name}-clickhouse" })
}

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }
}

resource "aws_security_group" "clickhouse" {
  name_prefix = "${var.cluster_name}-clickhouse-"
  vpc_id      = module.vpc.vpc_id

  ingress {
    from_port       = 8123
    to_port         = 8123
    protocol        = "tcp"
    security_groups = [module.eks.cluster_security_group_id]
    description     = "ClickHouse HTTP from EKS"
  }

  ingress {
    from_port       = 9000
    to_port         = 9000
    protocol        = "tcp"
    security_groups = [module.eks.cluster_security_group_id]
    description     = "ClickHouse native from EKS"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.common_tags
}

# ============================================================================
# Prometheus + Grafana (Amazon Managed Prometheus + Grafana)
# ============================================================================

resource "aws_prometheus_workspace" "main" {
  alias = "${var.cluster_name}-${var.environment}"

  tags = local.common_tags
}

resource "aws_grafana_workspace" "main" {
  name                     = "${var.cluster_name}-${var.environment}"
  account_access_type      = "CURRENT_ACCOUNT"
  authentication_providers = ["AWS_SSO"]
  permission_type          = "SERVICE_MANAGED"
  role_arn                 = aws_iam_role.grafana.arn

  data_sources = ["PROMETHEUS", "CLOUDWATCH"]

  tags = local.common_tags
}

resource "aws_iam_role" "grafana" {
  name = "${var.cluster_name}-grafana"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "grafana.amazonaws.com" }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "grafana_prometheus" {
  name = "${var.cluster_name}-grafana-prometheus"
  role = aws_iam_role.grafana.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "aps:QueryMetrics",
          "aps:GetSeries",
          "aps:GetLabels",
          "aps:GetMetricMetadata",
        ]
        Resource = [aws_prometheus_workspace.main.arn]
      },
      {
        Effect = "Allow"
        Action = [
          "cloudwatch:DescribeAlarmsForMetric",
          "cloudwatch:GetMetricData",
          "cloudwatch:GetMetricStatistics",
          "cloudwatch:ListMetrics",
        ]
        Resource = ["*"]
      },
    ]
  })
}

# ============================================================================
# Kubeflow (MLOps)
# ============================================================================

resource "kubernetes_namespace" "kubeflow" {
  metadata {
    name = "kubeflow"
    labels = {
      "istio-injection" = "enabled"
    }
  }

  depends_on = [module.eks]
}

resource "aws_iam_role" "kubeflow_training" {
  name = "${var.cluster_name}-kubeflow-training"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRoleWithWebIdentity"
      Effect = "Allow"
      Principal = {
        Federated = module.eks.oidc_provider_arn
      }
      Condition = {
        StringLike = {
          "${module.eks.oidc_provider}:sub" = "system:serviceaccount:kubeflow:*"
        }
      }
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "kubeflow_s3" {
  name = "${var.cluster_name}-kubeflow-s3"
  role = aws_iam_role.kubeflow_training.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["s3:GetObject", "s3:PutObject", "s3:ListBucket", "s3:DeleteObject"]
        Resource = [
          aws_s3_bucket.models.arn,
          "${aws_s3_bucket.models.arn}/*",
          aws_s3_bucket.training_data.arn,
          "${aws_s3_bucket.training_data.arn}/*",
        ]
      },
    ]
  })
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

  depends_on = [module.eks]
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
# OpenTelemetry Collector (Helm)
# ============================================================================

resource "kubernetes_namespace" "observability" {
  metadata {
    name = "observability"
    labels = {
      "istio-injection" = "enabled"
    }
  }

  depends_on = [module.eks]
}

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

  set {
    name  = "config.exporters.prometheusremotewrite.endpoint"
    value = "${aws_prometheus_workspace.main.prometheus_endpoint}api/v1/remote_write"
  }

  depends_on = [kubernetes_namespace.observability]
}

# ============================================================================
# Outputs
# ============================================================================

output "cluster_endpoint" {
  value = module.eks.cluster_endpoint
}

output "cluster_name" {
  value = module.eks.cluster_name
}

output "rds_endpoint" {
  value = module.rds.db_instance_endpoint
}

output "redis_endpoint" {
  value = aws_elasticache_replication_group.redis.primary_endpoint_address
}

output "msk_bootstrap_brokers" {
  value = aws_msk_cluster.kafka.bootstrap_brokers
}

output "kubeflow_training_role_arn" {
  value = aws_iam_role.kubeflow_training.arn
}

output "ecr_repositories" {
  value = { for k, v in aws_ecr_repository.services : k => v.repository_url }
}

output "api_endpoint" {
  value = "https://api.${var.domain_name}"
}

output "grpc_endpoint" {
  value = "${aws_lb.grpc.dns_name}:50051"
}

output "clickhouse_address" {
  value = aws_instance.clickhouse.private_ip
}

output "temporal_endpoint" {
  value = "${aws_service_discovery_service.temporal.name}.${aws_service_discovery_private_dns_namespace.internal.name}:7233"
}

output "prometheus_endpoint" {
  value = aws_prometheus_workspace.main.prometheus_endpoint
}

output "grafana_endpoint" {
  value = aws_grafana_workspace.main.endpoint
}
