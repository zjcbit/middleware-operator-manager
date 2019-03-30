#!/bin/bash

docker build -f ./Dockerfile -t operator-manager:v1 .
docker tag operator-manager:v1 10.10.103.59/k8s-deploy/operator-manager:v1
docker push 10.10.103.59/k8s-deploy/operator-manager:v1
kubectl apply -f operator-manager.yaml

