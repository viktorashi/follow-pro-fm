resource "fly_app" "poller" {
  name = var.poller_app_name
  org  = var.org_slug
}

resource "fly_ip" "poller_v4" {
  app  = fly_app.poller.name
  type = "shared_v4"

  lifecycle {
    ignore_changes = [type]
  }
}

resource "fly_ip" "poller_v6" {
  app  = fly_app.poller.name
  type = "public_v6"
}

resource "fly_volume" "poller_data" {
  app     = fly_app.poller.name
  name    = var.volume_name
  region  = var.region
  size_gb = var.volume_size_gb

  lifecycle {
    ignore_changes = [require_unique_zone]
  }
}

resource "fly_secret" "sendgrid_api_key" {
  count            = var.sendgrid_api_key != "" ? 1 : 0
  app              = fly_app.poller.name
  name             = "SENDGRID_API_KEY"
  value_wo         = var.sendgrid_api_key
  value_wo_version = sha256(var.sendgrid_api_key)
}

resource "fly_secret" "telegram_bot_token" {
  count            = var.telegram_bot_token != "" ? 1 : 0
  app              = fly_app.poller.name
  name             = "TELEGRAM_BOT_TOKEN"
  value_wo         = var.telegram_bot_token
  value_wo_version = sha256(var.telegram_bot_token)
}

resource "fly_secret" "admin_password" {
  count            = var.admin_password != "" ? 1 : 0
  app              = fly_app.poller.name
  name             = "ADMIN_PASSWORD"
  value_wo         = var.admin_password
  value_wo_version = sha256(var.admin_password)
}

resource "fly_machine" "poller" {
  app    = fly_app.poller.name
  name   = var.poller_machine_name
  region = var.region
  image  = var.poller_image

  guest {
    cpu_kind  = var.poller_guest.cpu_kind
    cpus      = var.poller_guest.cpus
    memory_mb = var.poller_guest.memory_mb
  }

  env = var.poller_env

  mount {
    volume = fly_volume.poller_data.id
    path   = "/data"
  }

  restart {
    policy      = "on-failure"
    max_retries = 10
  }

  service {
    internal_port = 8080
    protocol      = "tcp"
    autostart     = true
    autostop      = "off"

    port {
      port     = 80
      handlers = ["http"]
    }

    port {
      port     = 443
      handlers = ["http", "tls"]
    }
  }

  depends_on = [
    fly_volume.poller_data
  ]
}
