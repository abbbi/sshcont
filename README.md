spawn throwaway sytemd based or non-systemd based containers using
ssh.

```
 export DOCKER_API_VERSION=1.45
 go run .
```

and then:

```
 ssh -l "jrei/systemd-debian" -o StrictHostKeychecking=no localhost -p 2222
```

or likewise:

```
RHEL:
 ssh -l "registry.access.redhat.com/ubi9/ubi-init:latest" -o StrictHostKeychecking=no localhost -p 2222
SLES:
 ssh -l "registry.suse.com/bci/bci-init:15.6" -o StrictHostKeychecking=no localhost -p 2222
```

regular:

```
ssh -l "debian:bookworm" -o StrictHostKeychecking=no localhost -p 2222
ssh -l "alpine:latest" -o StrictHostKeychecking=no localhost -p 2222
```
