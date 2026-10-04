variable "org_slug" {
  description = "Fly.io organization slug"
  type        = string
  default     = "viktorashi"
}

variable "region" {
  description = "Fly.io primary region (closest to target broadcast in Romania)"
  type        = string
  default     = "fra"
}

variable "poller_app_name" {
  description = "Application name for the pro-fm poller service"
  type        = string
  default     = "pro-fm-poller"
}

variable "whisper_app_name" {
  description = "Application name for the pro-fm whisper speech-to-text service"
  type        = string
  default     = "pro-fm-whisper"
}

variable "poller_machine_name" {
  description = "Machine name for the poller instance"
  type        = string
  default     = "ancient-sea-2438"
}

variable "whisper_machine_name" {
  description = "Machine name for the whisper instance"
  type        = string
  default     = "wandering-flower-4342"
}

variable "poller_image" {
  description = "Docker image for pro-fm-poller"
  type        = string
  default     = "registry.fly.io/pro-fm-poller:deployment-01M0X7PHHCCA747RMTSYFP26XJ"
}

variable "whisper_image" {
  description = "Docker image for pro-fm-whisper"
  type        = string
  default     = "registry.fly.io/pro-fm-whisper:deployment-01M0X7KHRB2ZV06QNAPTSQZCK3"
}

variable "volume_name" {
  description = "Persistent volume name for poller data storage"
  type        = string
  default     = "pro_fm_data"
}

variable "volume_size_gb" {
  description = "Persistent volume size in GiB"
  type        = number
  default     = 1
}

variable "poller_guest" {
  description = "Compute configuration for the poller Machine"
  type = object({
    cpu_kind  = string
    cpus      = number
    memory_mb = number
  })
  default = {
    cpu_kind  = "shared"
    cpus      = 1
    memory_mb = 256
  }
}

variable "whisper_guest" {
  description = "Compute configuration for the whisper Machine"
  type = object({
    cpu_kind  = string
    cpus      = number
    memory_mb = number
  })
  default = {
    cpu_kind  = "shared"
    cpus      = 1
    memory_mb = 1024
  }
}

variable "poller_env" {
  description = "Environment variables for pro-fm-poller"
  type        = map(string)
  default = {
    TZ               = "Europe/Bucharest"
    ENVIRONMENT      = "production"
    TELEGRAM_CHAT_ID = "5943195496"
    EMAIL_FROM       = "ioanvictorstan@gmail.com"
    TARGET_PHONE     = "+40771001872"
  }
}

variable "whisper_env" {
  description = "Environment variables for pro-fm-whisper"
  type        = map(string)
  default = {
    UVICORN_HOST              = "0.0.0.0"
    LOOPBACK_HOST_URL         = "http://127.0.0.1:8000"
    ENABLE_UI                 = "false"
    WHISPER__INFERENCE_DEVICE = "cpu"
    WHISPER__COMPUTE_TYPE     = "int8"
    WHISPER__CPU_THREADS      = "1"
    OMP_NUM_THREADS           = "1"
  }
}

variable "sendgrid_api_key" {
  description = "Optional SendGrid API Key for magic login link emails (staged as Fly secret if set)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "telegram_bot_token" {
  description = "Optional Telegram Bot Token for notification alerts (staged as Fly secret if set)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "admin_password" {
  description = "Optional Admin password for web UI authentication (staged as Fly secret if set)"
  type        = string
  default     = ""
  sensitive   = true
}
