# w12e evidence

Evidence for the riskiest assumptions of docs/dforge/plans/2026-11-14-v1-w12e-security-residuals.md (Task 1), gathered on 2026-11-15 on the development host.

## Route capture (2026-11-15)

Read-only: `ip -6` commands and `netstat.RouteGet` from a temporary build-tagged test (`go test -tags w12ecapture -run TestW12eCapture ./internal/platform/netstat/`), deleted before this commit. Every source is a link-local or loopback IPv6 address of this host.

```text
$ ip -6 addr
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 state UNKNOWN qlen 1000
    inet6 ::1/128 scope host noprefixroute 
       valid_lft forever preferred_lft forever
2: eth0@veth1488da1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP qlen 1000
    inet6 fe80::be24:11ff:fedb:56d4/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
3: br-821275ad6659: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::cc28:d4ff:fec3:7683/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
4: br-a2871ddf38b3: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::74fa:8dff:fe89:221e/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
5: br-e6530ed1879a: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::7cb4:41ff:fea9:82cf/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
6: br-f97b8474ff04: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::5458:89ff:fe97:1979/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
7: br-139bdfd2f0d6: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::58a3:83ff:fe6e:f595/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
8: docker0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::10ed:bdff:fe67:f4d1/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
10: br-b5f8c8ad6dba: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::24d5:c2ff:fe0f:8843/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
11: br-3f98992d4b43: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::d80b:4fff:fe6e:cf9d/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
13: br-66c76e1edf91: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::4c34:71ff:fe1d:923b/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
15: vetha7a815a@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::5c35:5eff:fe8e:eddc/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
16: veth9667876@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::2c73:25ff:fe4c:2fa6/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
17: vethd42fe14@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::8421:5ff:fef3:f568/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
18: veth659351a@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::dcf8:7fff:fe20:eb71/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
21: veth888252f@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::9cad:48ff:fe59:9bb5/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
23: veth1488da1@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::ec98:8fff:fe5d:4be5/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
24: veth35eb98d@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::1484:33ff:fe5b:7b4c/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
25: veth1da9764@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::e431:34ff:fec3:1371/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
26: vethe5fe665@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::58f7:5ff:fe7d:8cd7/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
27: vethf8fe1a2@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::c84e:69ff:fe81:c322/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
28: vethbce6b97@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::2436:88ff:feab:703/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
29: veth57a47ee@br-821275ad6659: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::9c4d:e7ff:fee6:5739/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
33: veth9c2a232@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::283f:e8ff:fe20:a39d/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
37: vethde2df9a@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::9488:b4ff:fe5d:1c6f/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
4418: veth9097dcb@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::6404:8ff:feeb:737e/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
4420: veth632f23d@eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP 
    inet6 fe80::9057:f1ff:fe1d:5316/64 scope link proto kernel_ll 
       valid_lft forever preferred_lft forever
$ ip -6 route
fe80::/64 dev eth0 proto kernel metric 256 pref medium
fe80::/64 dev docker0 proto kernel metric 256 pref medium
fe80::/64 dev veth659351a proto kernel metric 256 pref medium
fe80::/64 dev br-139bdfd2f0d6 proto kernel metric 256 pref medium
fe80::/64 dev veth9667876 proto kernel metric 256 pref medium
fe80::/64 dev br-e6530ed1879a proto kernel metric 256 pref medium
fe80::/64 dev vethd42fe14 proto kernel metric 256 pref medium
fe80::/64 dev vetha7a815a proto kernel metric 256 pref medium
fe80::/64 dev br-f97b8474ff04 proto kernel metric 256 pref medium
fe80::/64 dev br-66c76e1edf91 proto kernel metric 256 pref medium
fe80::/64 dev br-a2871ddf38b3 proto kernel metric 256 pref medium
fe80::/64 dev veth888252f proto kernel metric 256 pref medium
fe80::/64 dev veth1488da1 proto kernel metric 256 pref medium
fe80::/64 dev br-b5f8c8ad6dba proto kernel metric 256 pref medium
fe80::/64 dev br-821275ad6659 proto kernel metric 256 pref medium
fe80::/64 dev veth35eb98d proto kernel metric 256 pref medium
fe80::/64 dev veth1da9764 proto kernel metric 256 pref medium
fe80::/64 dev vethe5fe665 proto kernel metric 256 pref medium
fe80::/64 dev vethf8fe1a2 proto kernel metric 256 pref medium
fe80::/64 dev vethbce6b97 proto kernel metric 256 pref medium
fe80::/64 dev veth57a47ee proto kernel metric 256 pref medium
fe80::/64 dev br-3f98992d4b43 proto kernel metric 256 pref medium
fe80::/64 dev veth9c2a232 proto kernel metric 256 pref medium
fe80::/64 dev vethde2df9a proto kernel metric 256 pref medium
fe80::/64 dev veth9097dcb proto kernel metric 256 pref medium
fe80::/64 dev veth632f23d proto kernel metric 256 pref medium
$ ip -6 route get 2001:db8::1
RTNETLINK answers: Network is unreachable
$ ip -6 route get fe80::1
fe80::1 from :: dev eth0 proto kernel src fe80::be24:11ff:fedb:56d4 metric 256 pref medium
```

One line per `netstat.RouteGet` lookup, `<dst> from <src|-> -> <route> <error> [<errors.Is classes>]`:

```text
2001:db8::1 from - -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from ::1 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::be24:11ff:fedb:56d4 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::cc28:d4ff:fec3:7683 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::74fa:8dff:fe89:221e -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::7cb4:41ff:fea9:82cf -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::5458:89ff:fe97:1979 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::58a3:83ff:fe6e:f595 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::10ed:bdff:fe67:f4d1 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::24d5:c2ff:fe0f:8843 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::d80b:4fff:fe6e:cf9d -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::4c34:71ff:fe1d:923b -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::5c35:5eff:fe8e:eddc -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::2c73:25ff:fe4c:2fa6 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::8421:5ff:fef3:f568 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::dcf8:7fff:fe20:eb71 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::9cad:48ff:fe59:9bb5 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::ec98:8fff:fe5d:4be5 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::1484:33ff:fe5b:7b4c -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::e431:34ff:fec3:1371 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::58f7:5ff:fe7d:8cd7 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::c84e:69ff:fe81:c322 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::2436:88ff:feab:703 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::9c4d:e7ff:fee6:5739 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::283f:e8ff:fe20:a39d -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::9488:b4ff:fe5d:1c6f -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::6404:8ff:feeb:737e -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2001:db8::1 from fe80::9057:f1ff:fe1d:5316 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2001:db8::1: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from - -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from ::1 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::be24:11ff:fedb:56d4 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::cc28:d4ff:fec3:7683 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::74fa:8dff:fe89:221e -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::7cb4:41ff:fea9:82cf -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::5458:89ff:fe97:1979 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::58a3:83ff:fe6e:f595 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::10ed:bdff:fe67:f4d1 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::24d5:c2ff:fe0f:8843 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::d80b:4fff:fe6e:cf9d -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::4c34:71ff:fe1d:923b -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::5c35:5eff:fe8e:eddc -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::2c73:25ff:fe4c:2fa6 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::8421:5ff:fef3:f568 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::dcf8:7fff:fe20:eb71 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::9cad:48ff:fe59:9bb5 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::ec98:8fff:fe5d:4be5 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::1484:33ff:fe5b:7b4c -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::e431:34ff:fec3:1371 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::58f7:5ff:fe7d:8cd7 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::c84e:69ff:fe81:c322 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::2436:88ff:feab:703 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::9c4d:e7ff:fee6:5739 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::283f:e8ff:fe20:a39d -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::9488:b4ff:fe5d:1c6f -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::6404:8ff:feeb:737e -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
2606:4700::1111 from fe80::9057:f1ff:fe1d:5316 -> {Type: Table:0 Protocol:0 Dst:invalid Prefix Gateway:invalid IP PrefSrc:invalid IP OutIndex:0 OutIface: Priority:0} netstat: no route to 2606:4700::1111: network is unreachable [noroute=true enetunreach=true ehostunreach=false eacces=false]
fe80::1 from - -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from ::1 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::be24:11ff:fedb:56d4 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::cc28:d4ff:fec3:7683 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::74fa:8dff:fe89:221e -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::7cb4:41ff:fea9:82cf -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::5458:89ff:fe97:1979 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::58a3:83ff:fe6e:f595 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::10ed:bdff:fe67:f4d1 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::24d5:c2ff:fe0f:8843 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::d80b:4fff:fe6e:cf9d -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::4c34:71ff:fe1d:923b -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::5c35:5eff:fe8e:eddc -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::2c73:25ff:fe4c:2fa6 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::8421:5ff:fef3:f568 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::dcf8:7fff:fe20:eb71 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::9cad:48ff:fe59:9bb5 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::ec98:8fff:fe5d:4be5 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::1484:33ff:fe5b:7b4c -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::e431:34ff:fec3:1371 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::58f7:5ff:fe7d:8cd7 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::c84e:69ff:fe81:c322 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::2436:88ff:feab:703 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::9c4d:e7ff:fee6:5739 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::283f:e8ff:fe20:a39d -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::9488:b4ff:fe5d:1c6f -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::6404:8ff:feeb:737e -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from fe80::9057:f1ff:fe1d:5316 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
fe80::1 from preferred fe80::be24:11ff:fedb:56d4 -> {Type:unicast Table:254 Protocol:2 Dst:fe80::1/128 Gateway:invalid IP PrefSrc:fe80::be24:11ff:fedb:56d4 OutIndex:2 OutIface:eth0 Priority:256} <nil> [noroute=false enetunreach=false ehostunreach=false eacces=false]
```

Gate D4: passes. All 56 global lookups (`2001:db8::1` and `2606:4700::1111`, without a source and from each of the 27 sources) answer `ENETUNREACH`, which `netstat.RouteGet` wraps as `ErrNoRoute`; no answer depends on the source. All 29 `fe80::1` lookups answer a unicast route through `eth0` with preferred source `fe80::be24:11ff:fedb:56d4`, whatever the source.

## NPM 2.16.0 image

Read on 2026-11-15 from the registry without Docker: index `sha256:4393e642e233e5efd5adeb7f10918d4aaea71c9ed875b94190273fc8e87f9594` (its digest checked), `linux/amd64` manifest `sha256:57d85bffc776c01909c7b24e127a69a72b7d8c75d387e6cb26c762978fc43e4c`, 32 layers; `app/package.json` says `"version": "2.16.0"`. Paths below are inside the image.

### 1. What a proxy-host update runs

`PUT /api/nginx/proxy-hosts/:id` calls `internalProxyHost.update` and sends its result with status 200 (`/app/routes/nginx/proxy_hosts.js:127-132`). After the row is patched, `update` skips nginx for a disabled host and otherwise runs `internalNginx.configure`, answering with the new `meta` (`/app/internal/proxy-host.js:212-217`):

```js
					if (!row.enabled) {
						// No need to add nginx config if host is disabled
						return row;
					}
					// Configure nginx
					return internalNginx.configure(proxyHostModel, "proxy_host", row).then((new_meta) => {
```

`configure` tests nginx, deletes and regenerates the host's file, tests again, records the result in `meta`, and reloads (`/app/internal/nginx.js:32-102`):

```js
		return internalNginx
			.test()
			...
				return internalNginx.generateConfig(host_type, host);
			...
				return internalNginx
					.test()
					.then(() => {
						// nginx is ok
						combined_meta = _.assign({}, host.meta, {
							nginx_online: true,
							nginx_err: null,
						});
			...
						// config is bad, update meta and delete config
						combined_meta = _.assign({}, host.meta, {
							nginx_online: false,
							nginx_err: valid_lines.join("\n"),
						});
			...
			.then(() => {
				return internalNginx.reload();
			})
```

`test` is `nginx -t -g "error_log off;"` and `reload` tests again, then runs `nginx -s reload` (`/app/internal/nginx.js:108-121`). So an unchanged update regenerates the host's file, tests nginx and reloads it. A failed second test is answered with success and `meta.nginx_online: false`, `meta.nginx_err: <nginx -t output>`; the failed file is renamed to `.err` and nginx is reloaded without it. A failed first test (another host's file already breaks `nginx -t`) rejects the whole update, so the API answers an error rather than success. A disabled host is skipped: no configure, no reload.

Gate D1: passes.

### 2. `enabled` in the answer

The column is an integer (`/app/migrations/20190104035154_disabled.js:18`, `proxy_host.integer("enabled").notNull().unsigned().defaultTo(1);`). The model converts it to a boolean when it reads a row: `enabled` is in `boolFields` (`/app/models/proxy_host.js:14-21`) and `$parseDatabaseJson` returns `convertIntFieldsToBool(thisJson, boolFields)` (`:56`), which sets `obj[field] = obj[field] === 1;` (`/app/lib/helpers.js:35`). The schema declares it `"type": "boolean"` (`/app/schema/common.json`, `properties.enabled`). The API answers a boolean; `npmapi.Flag` accepts 0/1 as well.

### 3. Static and resolved forward hosts

A custom location renders a static `proxy_pass` with the forward host (`/app/templates/_location.conf:10`):

```
    proxy_pass       {{ forward_scheme }}://{{ forward_host }}:{{ forward_port }}{{ forward_path }};
```

Before rendering, a location's forward host is cut at its first `/` and the rest becomes the path (`/app/internal/nginx.js:190-194`):

```js
					if (locationCopy.forward_host.indexOf("/") > -1) {
						const splitted = locationCopy.forward_host.split("/");

						locationCopy.forward_host = splitted.shift();
						locationCopy.forward_path = `/${splitted.join("/")}`;
```

The proxy host's own forward host is not static. The server block sets it in a variable (`/app/templates/proxy_host.conf:9`, `set $server         "{{ forward_host }}";`) and the default location proxies through variables (`/etc/nginx/conf.d/include/proxy.conf:7`, `proxy_pass       $forward_scheme://$server:$port$request_uri;`). nginx resolves a name in a `proxy_pass` with variables per request through `resolver` ("Parameter value can contain variables. In this case, if an address is specified as a domain name, the name is searched among the described server groups, and, if not found, is determined using a L<ngx_http_core_module>", the link being the `resolver` directive; `/etc/nginx/pod/nginx/ngx_http_proxy_module.pod`, `proxy_pass`). The resolver is written at container start from `/etc/resolv.conf` with a 10-second cache (`/etc/s6-overlay/s6-rc.d/prepare/40-dynamic.sh:12-14`, `echo resolver "..." valid=10s;`; included by `/etc/nginx/nginx.conf:53`). The whole server block renders only when the host is enabled (`/app/templates/proxy_host.conf:3`, `{% if enabled %}`).

Consequence for D1: a reload re-resolves the custom locations' static names. The default location's name is not fixed at reload; nginx looks it up again at most 10 seconds after the previous answer, so after a network join it follows the new answers within 10 seconds whether or not nginx is reloaded. The held-address check after the join compares fresh answers, which is what the default location will use.

### 4. Reloads NPM makes on its own

Every 6 hours NPM fetches the CDN IP ranges and, after the first run, reloads nginx (`/app/internal/ip_ranges.js:22`, `interval_timeout: 1000 * 60 * 60 * 6, // 6 hours`; `:111-113`, `if (internalIpRanges.iteration_count) {` then `// Reload nginx` and `return internalNginx.reload();`). A certificate request reloads nginx before and after the request and on failure (`/app/internal/certificate.js:155-184`, `await internalNginx.reload();`), and the renewal timer runs every hour (`/app/internal/certificate.js:33`, `intervalTimeout: 1000 * 60 * 60, // 1 hour`).

### 5. A static name with no answer

The image does not say it in its own text; this is nginx's load-time behaviour (inference from nginx's upstream parsing, not quoted from the image). A static `proxy_pass` name is resolved when the configuration is loaded, and a name with no answer fails the load with `[emerg] host not found in upstream`, so `nginx -t` fails, a reload is refused, and nginx does not start. The image starts nginx directly (`/etc/s6-overlay/s6-rc.d/nginx/run`, `set -e` then `exec s6-setuidgid "$PUID:$PGID" nginx`), so a custom location whose name stops answering keeps nginx from starting after a container restart. While nginx runs, the same file breaks the first `nginx -t` of every later update (question 1), so each later proxy-host update is answered with an error. The threat model states this; Task 7's late check refuses such a name only when it can resolve it.

### 6. Request body buffering

No configuration in the image sets `proxy_request_buffering` (no match under `/etc/nginx` and `/app/templates` outside the bundled documentation), so nginx's default applies: `default: on` (`/etc/nginx/pod/nginx/ngx_http_proxy_module.pod`, `proxy_request_buffering`), "When buffering is enabled, the entire request body is read from the client before sending the request to a proxied server." `client_max_body_size` is `2000m` (`/etc/nginx/nginx.conf:39`).

## Revert check (2026-11-15, w12f)

Each fix was undone on top of `w12f-low-rows` and its named regression test run alone; every one failed, and the tree was restored after each. Where a fix commit no longer reverse-applies (later commits touched its lines), the fix's key line was disabled instead.

| Named test | Fix undone | Result |
|---|---|---|
| TestW12eAttachReloadsBeforeJoin | `reloadBeforeJoin` skips its reload (network_claims.go) | FAIL |
| TestW12eAttachReloadsAfterJoin | `joinedProblem` skips the reload after the join | FAIL |
| TestW12eAttachUnconfirmedReloadDisconnects | `afterJoin` skips `disconnectAgain` | FAIL |
| TestW12eUpgradeUndoChecksForwards | `Inverse` skips `undoForwardCheck` (upgrade_recover.go) | FAIL |
| TestW12eUpgradeLateForwardCheck | `Apply`'s check after the pull lists targets without checking them (instance_upgrade.go) | FAIL |
| TestW12eStartVerifiesHeldAddresses | the upgrade's recreate skips `verifyHeld`; separately, the original's start skips it | FAIL, FAIL |
| TestW12eRestoredLocationCutAtSlash | b3bf7200 reverse-applied: proxyhost_edit.go checks a restored location's whole forward host again, not its part before `/` | FAIL |
| TestW12eLinkLocalOnlyHostGuardsIPv6443 | f4d3f5d5 reverse-applied: guard/flows.go builds no IPv6 NPM flow on a host whose only IPv6 addresses are link-local | FAIL |
| TestW12eTailnetClientOfAnExposure | linkguard.go skips the NPM client flow | FAIL |

## Not run on this host

The A3a demo (a proxy-host update against a running NPM from the pinned image) and the R32 try (disconnecting `darkpanel-npm` from its network on the pinned Engine) were not run, because this host forbids talking to its Docker daemon. The image source reading above (answers 1-3) and the fake NPM and test Engine tests of Tasks 5-7 (`TestW12eVerifyHeldFailures` for R32) stood in for them.

Owner ruling 2026-11-15 (security row 181): the R32 substitution is accepted; the A3a substitution is not. A3a is proven by the `reload-follows-name` step of `TestM6NPM` (test/harness/m6_npm_test.go), which runs against the pinned NPM on the CI VMs; its result is the gate 7 harness run of the w12f branch.
