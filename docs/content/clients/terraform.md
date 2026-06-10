---
title: "Terraform"
description: "managing githome resources with the terraform github provider"
weight: 30
---

The [Terraform GitHub provider](https://registry.terraform.io/providers/integrations/github/latest)
speaks the GitHub REST API and works with githome. Point `base_url` at your
host and use your PAT as the token.

## Provider configuration

```hcl
terraform {
  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.0"
    }
  }
}

provider "github" {
  token    = var.githome_token
  base_url = "https://git.example.com/"  # trailing slash is required
  owner    = "myorg"
}
```

Always pass the token through a variable. Never hard-code it in configuration
files.

```hcl
variable "githome_token" {
  description = "PAT for githome"
  type        = string
  sensitive   = true
}
```

Supply the value at plan/apply time:

```sh
export TF_VAR_githome_token="ghp_yourtoken"
terraform plan
```

or store it in a `terraform.tfvars` file that you keep out of version control:

```hcl
# terraform.tfvars  (add to .gitignore)
githome_token = "ghp_yourtoken"
```

## Resources

### github_repository

```hcl
resource "github_repository" "api" {
  name        = "api"
  description = "REST API service"
  visibility  = "private"
  auto_init   = true

  topics = ["go", "api", "rest"]
}
```

### github_repository_webhook

```hcl
resource "github_repository_webhook" "ci" {
  repository = github_repository.api.name

  configuration {
    url          = "https://ci.example.com/hook"
    content_type = "json"
    insecure_ssl = false
    secret       = var.webhook_secret
  }

  events = ["push", "pull_request"]
  active = true
}
```

### github_issue_label

```hcl
resource "github_issue_label" "bug" {
  repository = github_repository.api.name
  name       = "bug"
  color      = "d73a4a"
}

resource "github_issue_label" "enhancement" {
  repository = github_repository.api.name
  name       = "enhancement"
  color      = "a2eeef"
}
```

### github_team

```hcl
resource "github_team" "backend" {
  name        = "backend"
  description = "Backend engineers"
  privacy     = "closed"
}
```

### github_team_membership

```hcl
resource "github_team_membership" "alice" {
  team_id  = github_team.backend.id
  username = "alice"
  role     = "member"
}

resource "github_team_membership" "bob_maintainer" {
  team_id  = github_team.backend.id
  username = "bob"
  role     = "maintainer"
}
```

### github_branch_protection

```hcl
resource "github_branch_protection" "main" {
  repository_id = github_repository.api.node_id
  pattern       = "main"

  required_pull_request_reviews {
    required_approving_review_count = 1
    dismiss_stale_reviews           = true
  }

  required_status_checks {
    strict   = true
    contexts = ["ci/test"]
  }

  enforce_admins = true
}
```

## Data sources

```hcl
data "github_repository" "upstream" {
  full_name = "myorg/upstream"
}

output "upstream_clone_url" {
  value = data.github_repository.upstream.http_clone_url
}
```

```hcl
data "github_user" "alice" {
  username = "alice"
}

output "alice_node_id" {
  value = data.github_user.alice.node_id
}
```

```hcl
data "github_team" "backend" {
  slug = "backend"
}
```

## Full working example

The following configuration creates a repository, attaches labels, wires up a
webhook, and adds a team member.

```hcl
terraform {
  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.0"
    }
  }
}

variable "githome_token" {
  type      = string
  sensitive = true
}

variable "webhook_secret" {
  type      = string
  sensitive = true
}

provider "github" {
  token    = var.githome_token
  base_url = "https://git.example.com/"
  owner    = "myorg"
}

resource "github_repository" "service" {
  name       = "service"
  visibility = "private"
  auto_init  = true
  topics     = ["go", "microservice"]
}

resource "github_issue_label" "bug" {
  repository = github_repository.service.name
  name       = "bug"
  color      = "d73a4a"
}

resource "github_issue_label" "feature" {
  repository = github_repository.service.name
  name       = "feature"
  color      = "0075ca"
}

resource "github_repository_webhook" "deploy" {
  repository = github_repository.service.name

  configuration {
    url          = "https://deploy.example.com/hook"
    content_type = "json"
    secret       = var.webhook_secret
    insecure_ssl = false
  }

  events = ["push", "release"]
  active = true
}

resource "github_team" "devs" {
  name    = "devs"
  privacy = "closed"
}

resource "github_team_membership" "alice" {
  team_id  = github_team.devs.id
  username = "alice"
  role     = "member"
}
```

## Importing existing resources

Bring an existing repository under Terraform management without recreating it:

```sh
terraform import github_repository.example owner/myrepo
```

Import a webhook by repository name and numeric webhook ID:

```sh
terraform import github_repository_webhook.ci myrepo/123456
```

Import an issue label by `repository:label-name`:

```sh
terraform import github_issue_label.bug myrepo:bug
```

After importing, run `terraform plan` to confirm no unintended changes are
pending before your first `apply`.

## Known limitations

The following GitHub provider resources and features are not yet supported by
githome and will return errors if applied:

- `github_actions_secret` and `github_actions_variable`: Actions secrets/variables API not implemented.
- `github_repository_ruleset`: Repository rulesets are not yet implemented.
- `github_repository_environment`: Deployment environments are not yet implemented.
- `github_app_installation_repository`: GitHub Apps installation API is partially implemented; repository-level installation scoping is not supported.
- `github_repository_dependabot_security_updates`: Dependabot is not implemented.
- `github_codespaces_*`: Codespaces are not implemented.

Resources that rely on git operations under the hood (branch protection,
webhooks, labels, teams, memberships, and repository CRUD) all work.
