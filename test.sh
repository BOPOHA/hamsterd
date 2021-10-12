#!/bin/bash

export https_proxy=0:8080
TESTURL=https://d2lzkl7pfhq30w.cloudfront.net:443/pub/fedora/linux/releases/33/Everything/x86_64/os/repodata/cc9d14bdf2f810f08e5cc3a0aed403212ae73fb492720ed3adaa2a9ce7581352-primary.xml.zck
TESTURL=https://d2lzkl7pfhq30w.cloudfront.net/pub/fedora/linux/releases/33/Everything/x86_64/os/repodata/f9196996d311c31b162864369f45430463808a93e8c5ab92bede63611698358e-comps-Everything.x86_64.xml.zck


curl -ivv ${TESTURL} -o /dev/null --http2-prior-knowledge
