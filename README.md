# HAMSTER is a http/https-proxy with caching transport

mock -r epel-8-x86_64 \
    --scm-enable \
    --scm-option method=git \
    --scm-option package=hamsterd \
    --scm-option spec=build/rpm/hamsterd.spec \
    --scm-option branch=dev \
    --scm-option write_tar=True \
    --scm-option git_get='git clone https://github.com/BOPOHA/hamsterd.git'
