### middleware-operator-manager
中间件operator

## 编译前提
`提前安装好go语言开发环境，正确设置GOROOT和GOPATH环境变量，要求go1.8.3版本以下`

## 编译二进制
将middleware-operator-manager放在$GOPATH/src/harmonycloud.cn/目录下，进入到
$GOPATH/src/harmonycloud.cn/middleware-operator-manager/cmd/operator-manager目录，
最终要生成linux的可执行文件：

- 如果是在windows上编译：

打开cmd窗口，进入以上目录后，执行以下命令：
```sh
set GOOS=linux
go build -a -o operator-manager
```


- 如果是在linux上编译：

执行以下命令：
```sh
go build -a -o operator-manager
```
等待编译完成，最终在当前目录下生成operator-manager可执行文件

## 编译镜像和部署

`前提安装了docker和k8s集群` 

$GOPATH/src/harmonycloud.cn/middleware-operator-manager/artifacts目录下有Dockerfile文件，基础镜像为busybox
```sh
FROM busybox

ADD operator-manager /usr/bin/ 
RUN chmod +x /usr/bin/operator-manager
```
同级目录下有operator-manager deployment描述文件operator-manager.yaml:
```yaml
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  generation: 2
  labels:
    app: operator-manager
  name: operator-manager
  namespace: kube-system
spec:
  replicas: 2
  selector:
    matchLabels:
      app: operator-manager
  strategy:
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 1
    type: RollingUpdate
  template:
    metadata:
      creationTimestamp: null
      labels:
        app: operator-manager
    spec:
      containers:
      - command:
        - operator-manager
        - --v=5
        - --leader-elect=true
        image: 192.168.26.46/k8s-deploy/operator-manager:v1
        resources:
          limits:
            cpu: 500m
            memory: 512Mi
          requests:
            cpu: 200m
            memory: 512Mi
        imagePullPolicy: Always
        name: operator-manager
        terminationMessagePath: /dev/termination-log
        terminationMessagePolicy: File
      dnsPolicy: ClusterFirst
      restartPolicy: Always
      schedulerName: default-scheduler
      securityContext: {}
      terminationGracePeriodSeconds: 30
```

同级目录下有build.sh脚本，指定了docker镜像仓库地址为192.168.26.46
```sh
#!/bin/bash

docker build -f ./Dockerfile -t operator-manager:v1 .
docker tag operator-manager:v1 192.168.26.46/k8s-deploy/operator-manager:v1
docker push 192.168.26.46/k8s-deploy/operator-manager:v1
kubectl apply -f operator-manager.yaml
```
执行该脚本即可以将operator-manager二进制打成镜像并推送到192.168.26.46仓库的k8s-deploy项目下：
同时执行了
```sh
kubectl apply -f operator-manager.yaml
```
命令创建了operator-manager的deployment对象，完成了部署。



