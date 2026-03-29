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

variable "aws_region" {
  default = "us-west-2"
}

variable "environment" {
  default = "dev"
}

variable "cluster_name" {
  default = "fleetos"
}

# --- VPC ---
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

  tags = {
    Environment = var.environment
    Project     = "fleetos"
  }
}

# --- EKS Cluster ---
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
    # General workloads (API, ingestion)
    general = {
      instance_types = ["m6i.xlarge"]
      min_size       = 2
      max_size       = 10
      desired_size   = 3

      labels = {
        workload = "general"
      }
    }

    # GPU nodes for inference
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

    # High-memory nodes for Kafka
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

  tags = {
    Environment = var.environment
    Project     = "fleetos"
  }
}

# --- RDS PostgreSQL ---
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

  # Password managed via AWS Secrets Manager (not hardcoded)
  manage_master_user_password = true

  multi_az = var.environment != "dev"

  # Security
  storage_encrypted = true
  vpc_security_group_ids = [aws_security_group.rds.id]
  subnet_ids             = module.vpc.private_subnets

  family               = "postgres16"
  major_engine_version = "16"

  # Backups
  backup_retention_period  = var.environment == "prod" ? 30 : 7
  backup_window            = "03:00-04:00"
  maintenance_window       = "Mon:04:00-Mon:05:00"
  deletion_protection      = var.environment == "prod"

  # Monitoring
  monitoring_interval = var.environment == "prod" ? 60 : 0
  enabled_cloudwatch_logs_exports = ["postgresql", "upgrade"]

  tags = {
    Environment = var.environment
    Project     = "fleetos"
  }
}

# --- ElastiCache Redis ---
resource "aws_elasticache_replication_group" "redis" {
  replication_group_id = "${var.cluster_name}-redis"
  description          = "FleetOS Redis cluster"

  node_type            = var.environment == "dev" ? "cache.t4g.small" : "cache.r6g.large"
  num_cache_clusters   = var.environment == "dev" ? 1 : 3

  engine_version       = "7.0"
  port                 = 6379

  subnet_group_name    = aws_elasticache_subnet_group.redis.name
  security_group_ids   = [module.eks.cluster_security_group_id]

  automatic_failover_enabled    = var.environment != "dev"
  at_rest_encryption_enabled    = true
  transit_encryption_enabled    = var.environment != "dev"

  tags = {
    Environment = var.environment
    Project     = "fleetos"
  }
}

resource "aws_elasticache_subnet_group" "redis" {
  name       = "${var.cluster_name}-redis"
  subnet_ids = module.vpc.private_subnets
}

# --- RDS Security Group ---
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

  tags = {
    Environment = var.environment
    Project     = "fleetos"
  }
}

# --- S3 Buckets (encrypted + private) ---
locals {
  s3_buckets = {
    telemetry     = "${var.cluster_name}-telemetry-${var.environment}"
    models        = "${var.cluster_name}-models-${var.environment}"
    training_data = "${var.cluster_name}-training-data-${var.environment}"
  }
}

resource "aws_s3_bucket" "telemetry" {
  bucket = local.s3_buckets["telemetry"]
  tags   = { Environment = var.environment, Project = "fleetos" }
}

resource "aws_s3_bucket" "models" {
  bucket = local.s3_buckets["models"]
  tags   = { Environment = var.environment, Project = "fleetos" }
}

resource "aws_s3_bucket" "training_data" {
  bucket = local.s3_buckets["training_data"]
  tags   = { Environment = var.environment, Project = "fleetos" }
}

# S3 encryption (SSE-S3 for all buckets)
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

# S3 public access block (all buckets)
resource "aws_s3_bucket_public_access_block" "all" {
  for_each = local.s3_buckets
  bucket   = each.value

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true

  depends_on = [aws_s3_bucket.telemetry, aws_s3_bucket.models, aws_s3_bucket.training_data]
}

# S3 versioning (models bucket only — for model rollback)
resource "aws_s3_bucket_versioning" "models" {
  bucket = aws_s3_bucket.models.id
  versioning_configuration {
    status = "Enabled"
  }
}

# S3 lifecycle (telemetry: archive to Glacier after 90 days, delete after 365)
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

# --- Outputs ---
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
