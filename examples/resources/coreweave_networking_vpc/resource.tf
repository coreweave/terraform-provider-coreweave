resource "coreweave_networking_vpc" "example" {
  name = "default"
  zone = "US-EAST-04A"

  host_prefixes = [
    {
      name = "primary"
      type = "PRIMARY"
      prefixes = [
        "10.16.192.0/18",
        "2601:db8:aaaa::/48",
      ]
    },
    {
      name = "container-network"
      type = "ROUTED"
      prefixes = [
        "2601:db8:bbbb::/48"
      ]
      ipam = {
        prefix_length          = 80
        gateway_address_policy = "FIRST_IP" # Other options available, see docs for details
      }
    },
    {
      name = "attached-network"
      type = "ATTACHED"
      prefixes = [
        "2601:db8:cccc::/48"
      ]
      ipam = {
        prefix_length = 64
      }
    },
  ]

  # Existing untyped prefixes can remain untyped. Type changes require VPC API support.
  # For a new VPC, these prefixes may use POD, SERVICE, and LOAD_BALANCER types.
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
    {
      name  = "private-access"
      value = "10.48.0.0/24"
      type  = "PRIVATE_ACCESS"
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
