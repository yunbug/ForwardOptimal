# ForwardOptimal
ForwardOptimal是一个TCP转发工具，在首次启动后会对所有目标列表进行延迟检测，找出最佳最优选的IP目标，并对最优的目标进行TCP转发


## 前言：
   某一天，我在寻找一款能够检测目标延迟的TCP转发工具，并且能过在转发目标列表中找出最优(最低延迟)的IP进行转发，但不幸的是我找了很久，并没有很好的方案，那么此工具因此而生



## 介绍：
   ForwardOptimal 只是一个 TCP转发工具 ，
   ### 它的唯一功能便是在 “IP列表中寻找出延迟最低的IP进行TCP端口转发”，仅此而已！
   





## 使用：
### 运行方法：
   ./ForwardOptimal [您的文件路径]
   例子 ： ./ForwardOptimal /etc/ForwardOptimal/config.json

   如果不设置路径，那么默认会使用config.json
   
### 配置文件：
#### config.json文件 简单介绍


[前往 config.md](https://github.com/yunbug/ForwardOptimal/blob/main/config.md)




## 结尾
OK，到这里将结束了，再次提醒，它不支持轮训和负载均衡，也不支持热启动，也就是说，你每一次修改json文件，都需要重启程序才能生效，如有需要可自己修改。

需要注意的是，如果在运行中途 最优IP目标宕机了，那么转发将会无效，只能等到程序下一次延迟检查。

Releases 中的是 二进制文件，AMD架构的，其他架构自行编译

这只是一个很简单的转发工具，并没有所谓的那么强大高性能，甚至如果你是有几百个转发目标，我不并推荐你使用，它是逐行检查延迟，而不是异步多线程之类的！
！！！






.....
.
.
.
.....
## 补充 快速使用：

#### 下载

```
mkdir /etc/ForwardOptimal/
wget -P /etc/ForwardOptimal/ForwardOptimal "https://github.com/yunbug/ForwardOptimal/releases/download/v1.3.0/ForwardOptimal"
chmod 777 /etc/ForwardOptimal/ForwardOptimal
```

#### 编写json文件
```
cat > /etc/ForwardOptimal/config.json << EOF
{
    "bindAddr": ":8080",
    "targets": [
        "23.255.255.255:53",
        "66.20.21.11:3389"
        "[2401:1111:1::1111:1122]:3389"
        "[2401:2222:2::2222:3333]:80"
    ],
    "updateInterval": 60,
    "proxyProtocol": "v2"
}

EOF
```

此时只需要修好好json文件后
直接 /etc/ForwardOptimal/ForwardOptimal  即可运行
或指定目录，/etc/ForwardOptimal/ForwardOptimal /etc/ForwardOptimal/config.json 运行
下面是一些简单的守护

#### 进程守护
```
#(可选) curl -o /etc/systemd/system/ForwardOptimal.service https://raw.githubusercontent.com/yunbug/ForwardOptimal/refs/heads/main/ForwardOptimal.service
```

```
echo ' 
[Unit]
Description=ForwardOptimal TCP
After=network.target
Wants=network.target

[Service]
User=root
Group=root
Type=simple
LimitAS=infinity
LimitRSS=infinity
LimitCORE=infinity
LimitNOFILE=999999999
WorkingDirectory=/etc/ForwardOptimal/
ExecStart=/etc/ForwardOptimal/ForwardOptimal
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
' >/etc/systemd/system/ForwardOptimal.service
```
sudo systemctl daemon-reload

sudo systemctl start ForwardOptimal.service

sudo systemctl status ForwardOptimal.service



#### 设置开机自启
```
systemctl enable ForwardOptimal

```
