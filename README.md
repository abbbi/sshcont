spawn throwaway systemd or non-systemd based docker containers using ssh.

```
 go run .
```

use the following ssh command to start and enter an container:

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

# Notes:

* No authentication implemented, you should not run this on a public
* Container images not available on the host will be pulled.
* Container is removed after exiting the session.

# Why?

* I know [containerssh](https://github.com/containerssh) exists, but it brings
  way too much features i dont need.
* Sometimes i work on systems where docker is not available but need quick
  access to an container for testing.
