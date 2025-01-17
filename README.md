# sshcont

spawn throwaway systemd or non-systemd based docker containers using ssh.

# Usage:

```
sshcont:
  -bind string
        bind address, 127.0.0.1:2222, use :2222 for all (default "127.0.0.1:2222")
  -cmd string
        Execute cmd after login, example: ls
  -image string
        Force image to be executed
  -vol string
        Share volume into container, example: /home/:/home_shared
```


after starting the service, use the following ssh command to start and enter an
container:

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

# Executing scripts for CI testing

Currently it is not possible to specify a command for the ssh session, but the
`cmd` option can be used to execute an specified command within the container
for CI testing. Example:

```
 cat /tmp/ci/test.sh
 #!/bin/bash
 exit 1

 sshcon -vol /tmp/ci:/ci -cmd /ci/test.sh
 user@host: ~ $ ssh -l "debian:bookworm" -o StrictHostKeychecking=no localhost -p 2222
 Connection to localhost closed.
 user@host: ~ $ echo $?
 1
```

# Notes:

* No authentication implemented, you should not run this on a public network
  interface.
* Container images not available on the host will be pulled.
* Container is removed after exiting the session.

# Why?

* I know [containerssh](https://github.com/containerssh) exists, but it brings
  way too much features i dont need.
* Sometimes i work on systems where docker is not available but need quick
  access to an container for testing.
