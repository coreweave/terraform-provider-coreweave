resource "coreweave_networking_vpc" "example" {
  name        = "default"
  zone        = "US-EAST-04A"
  host_prefix = "10.16.192.0/18"
  vpc_prefixes = [
    {
      name  = "cidr a"
      value = "10.32.4.0/22"
    },
    {
      name  = "cidr b"
      value = "10.45.4.0/22"
    }
  ]

  egress = {
    disable_public_access = false
  }

  ingress = {
    disable_public_services = false
  }

  dhcp = {
    dns = {
      servers = ["1.1.1.1", "8.8.8.8"]
    }
  }
}
