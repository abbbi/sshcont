spawn shoft lived sytemd based or non-systemd based containers using
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
 ssh -l "registry.access.redhat.com/ubi9/ubi-init" -o StrictHostKeychecking=no localhost -p 2222
```

regular:

```
ssh -l "debian:bookworm" -o StrictHostKeychecking=no localhost -p 2222
```

