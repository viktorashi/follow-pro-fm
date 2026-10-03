output "poller_app_name" {
  description = "Fly.io app name for pro-fm-poller"
  value       = fly_app.poller.name
}

output "poller_url" {
  description = "Public URL for pro-fm-poller"
  value       = "https://${fly_app.poller.name}.fly.dev"
}

output "poller_machine_id" {
  description = "Machine ID for pro-fm-poller"
  value       = fly_machine.poller.id
}

output "poller_volume_id" {
  description = "Volume ID for pro-fm-poller data"
  value       = fly_volume.poller_data.id
}

output "whisper_app_name" {
  description = "Fly.io app name for pro-fm-whisper"
  value       = fly_app.whisper.name
}

output "whisper_flycast_address" {
  description = "Internal Flycast IPv6 address for pro-fm-whisper"
  value       = fly_ip.whisper_flycast.address
}

output "whisper_machine_id" {
  description = "Machine ID for pro-fm-whisper"
  value       = fly_machine.whisper.id
}
