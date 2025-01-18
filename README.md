# sshcont

spawn throwaway systemd or non-systemd based docker containers using ssh.

# Usage:

```
sshcont:
  -bind string
        bind address, 127.0.0.1:2222, use :2222 for all (default "127.0.0.1:2222")
  -cmd string
        Execute cmd after login, example: ls
  -export string
        Before removing, export container contents to specified directory, example: /tmp/
  -image string
        Force image to be executed
  -vol string
        Share volume into container, example: /home/:/home_shared
```


after starting the service, use the following ssh command to start and enter an
container:

```
 ssh -l "jrei/systemd-debian" -o StrictHostKeychecking=no localhost -p 2222
 root@89fb0de78a12:/
```

or likewise:

```
RHEL:
 ssh -l "registry.access.redhat.com/ubi9/ubi-init:latest" -o StrictHostKeychecking=no localhost -p 2222
 root@89fb0de78a14:/
SLES:
 ssh -l "registry.suse.com/bci/bci-init:15.6" -o StrictHostKeychecking=no localhost -p 2222
 root@89fb0de78a15:/
```

regular:

```
ssh -l "debian:bookworm" -o StrictHostKeychecking=no localhost -p 2222
 root@89fb0de78a16:/
ssh -l "alpine:latest" -o StrictHostKeychecking=no localhost -p 2222
 root@89fb0de78a17:/
```

# Executing scripts for CI testing

Execute predefined script by using the `cmd` option:

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

on multiple containers:

```
for dist in $(echo "debian:bookworm" "debian:buster" "debian:bullseye"
"alpine:latest" "registry.suse.com/bci/bci-init:15.6"); do
    ssh -l "$dist" -o StrictHostKeychecking=no localhost -p 2222;
done
```

or directly execute a command via ssh call:

```
 ssh -l "debian:bookworm" -o StrictHostKeychecking=no localhost -p 2222 ls; echo $?
 bin   dev  home  lib64  mnt  proc  run   srv  tmp  var
 boot  etc  lib   media  opt  root  sbin  sys  usr
 0
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
