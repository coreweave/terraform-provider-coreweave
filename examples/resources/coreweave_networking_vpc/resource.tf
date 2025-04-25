resource "coreweave_networking_vpc" "example" {
  name        = "default"
  zone        = "US-EAST-04A"
  host_prefix = "10.16.192.0/18"
  vpc_prefixes = [
    {
      name  = "pod cidr"
      value = "10.0.0.0/13"
    },
    {
      name  = "service cidr"
      value = "10.16.0.0/22"
    },
    {
      name  = "internal lb cidr"
      value = "10.32.4.0/22"
    },
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
