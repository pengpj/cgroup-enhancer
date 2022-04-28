# cgroup-enhancer

cgroup-enhancer

## 功能

### device.allow block 支持
在 Pod 的 annotation 中增加 key ：cgroup-enhancer.device.allow.block

value 值可参数：https://www.kernel.org/doc/Documentation/cgroup-v1/devices.txt

效果是会向 pod 所有 container 的 cgroup device.allow 写入 [value]

> 需要注意的是 major 和 minor 都是 *。

示例：
```shell
annotations:
  cgroup-enhancer.device.allow.block: "rmw"
```

实现效果，

```shell
cat /sys/fs/cgroup/devices/devices.list 
... 省略其它 ...
b *:* rwm
```

## 代码

代码放置到 `$GOPATH/src` 目录下

```shell
mkdir -p $GOPATH/src
git clone 
make build
```

## 部署

```shell
# 构建
make docker-build

# 镜像推送
docker push cgroup-enhancer:latest
```

集群部署

```shell
kubectl apply -f deploy/cgroup-enhancer-deploy.yaml
```
