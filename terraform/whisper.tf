resource "fly_app" "whisper" {
  name = var.whisper_app_name
  org  = var.org_slug
}

resource "fly_ip" "whisper_flycast" {
  app  = fly_app.whisper.name
  type = "private_v6"
}

resource "fly_machine" "whisper" {
  app    = fly_app.whisper.name
  name   = var.whisper_machine_name
  region = var.region
  image  = var.whisper_image

  guest {
    cpu_kind  = var.whisper_guest.cpu_kind
    cpus      = var.whisper_guest.cpus
    memory_mb = var.whisper_guest.memory_mb
  }

  env = var.whisper_env

  restart {
    policy      = "on-failure"
    max_retries = 10
  }

  service {
    internal_port = 8000
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

    check {
      type     = "http"
      port     = 8000
      path     = "/health"
      method   = "GET"
      interval = "15s"
      timeout  = "30s"
    }
  }

  depends_on = [
    fly_ip.whisper_flycast
  ]
}
