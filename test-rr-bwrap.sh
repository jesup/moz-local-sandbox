#!/bin/bash
# Test rr record inside a bwrap sandbox similar to ccode
RR=${1:-/home/padenot/src/repositories/rr/build/bin/rr}

bwrap \
    --ro-bind /usr /usr \
    --ro-bind /lib /lib \
    --ro-bind /lib64 /lib64 \
    --ro-bind /bin /bin \
    --ro-bind /etc/resolv.conf /etc/resolv.conf \
    --ro-bind /etc/hosts /etc/hosts \
    --ro-bind /etc/ssl /etc/ssl \
    --ro-bind /etc/passwd /etc/passwd \
    --ro-bind /etc/group /etc/group \
    --bind "$HOME/.local/share/rr" "$HOME/.local/share/rr" \
    --tmpfs /tmp \
    --proc /proc \
    --dev /dev \
    --setenv HOME "$HOME" \
    --share-net \
    --unshare-ipc \
    --unshare-uts \
    --unshare-cgroup-try \
    --ro-bind "$RR" /usr/local/bin/rr \
    /usr/local/bin/rr record /bin/true
