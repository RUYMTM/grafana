cd packaging/docker/custom
docker build --tag eu.gcr.io/ewiser/ewiser-grafana:1.1.0-9.2.2-ubuntu .
docker push eu.gcr.io/ewiser/ewiser-grafana:1.1.0-9.2.2
