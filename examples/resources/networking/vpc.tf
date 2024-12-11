resource "coreweave_networking_vpc" "test" {
  name          = "test"
  zone          = "US-EAST-04A"
  host_prefixes = ["10.16.192.0/18", "172.0.0.0/18"]
  vpc_prefixes = [
    {
      name  = "cidr a"
      value = "10.32.4.0/22"
    },
    {
      name                       = "cidr b"
      value                      = "10.45.4.0/22"
      disable_external_propagate = true
      disable_host_bgp_peering   = true
      host_dhcp_route            = true
      public                     = true
    }
  ]
  dns_servers = ["1.1.1.1", "8.8.8.8"]
}