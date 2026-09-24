#!/bin/bash

export https_proxy=0:8080
TESTURL=https://registry.npmjs.org/stringify-object/-/stringify-object-3.2.0.tgz

curl -i ${TESTURL}
