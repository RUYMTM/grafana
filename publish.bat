cd packaging/docker/custom
( cd packaging/docker/custom ; docker build --build-arg "GF_INSTALL_PLUGINS=yesoreyeram-infinity-datasource,grafana-singlestat-panel" --build-arg "GRAFANA_VERSION=8.5.1" --tag eu.gcr.io/ewiser/ewiser-grafana:1.1.0 . )
docker push eu.gcr.io/ewiser/ewiser-grafana:1.0.1d
