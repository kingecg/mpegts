# mpegts
mpegts plugin for m7s

support mpeg2-ts with http and ws

## Config

```yaml
mpegts:
  tinterval: 10 #插入pat,pmt的间隔时间,单位：秒
```

## 播放URL

插件支持ts流的http协议和websocket播放

url path:

/ts/<stream path>

## Api

无