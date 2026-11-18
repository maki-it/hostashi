#!/bin/bash
# usage: run.sh <image> <scenario>   (sandbox-only: proxy and CA bundle for the TLS-intercepting egress proxy)
IMG=$1; SC=$2; N=s10-$(echo $IMG | tr ':/' '--')-$SC; HERE=$(cd "$(dirname "$0")" && pwd)
docker rm -f $N >/dev/null 2>&1; docker run -d --name $N --network host $IMG sleep 3000 >/dev/null
docker exec $N mkdir -p /etc/ssl/certs; docker cp /root/.ccr/ca-bundle.crt $N:/etc/ssl/certs/ca-certificates.crt
docker exec $N bash -c "echo 'Acquire::https::Proxy \"$HTTPS_PROXY\";' > /etc/apt/apt.conf.d/99p"
docker cp $HERE/s10-test.sh $N:/s10-test.sh
docker exec -e HTTPS_PROXY=$HTTPS_PROXY -e CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt $N /s10-test.sh $SC
docker rm -f $N >/dev/null
