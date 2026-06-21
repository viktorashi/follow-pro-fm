with open("pkg/poller/server.go", "r") as f:
    c = f.read()

c = c.replace(
    '''\tprotected.POST("/api/kill-switch", s.handleKillSwitch)
}''',
    '''\tprotected.POST("/api/kill-switch", s.handleKillSwitch)
\tprotected.POST("/api/upload", s.handleUpload)
}'''
)

c = c.replace('defer src.Close()', 'defer func() { _ = src.Close() }()')
c = c.replace('defer dst.Close()', 'defer func() { _ = dst.Close() }()')
c = c.replace('buf.ReadFrom(src)', '_, _ = buf.ReadFrom(src)')
c = c.replace('dst.Write(buf.Bytes())', '_, _ = dst.Write(buf.Bytes())')

with open("pkg/poller/server.go", "w") as f:
    f.write(c)
