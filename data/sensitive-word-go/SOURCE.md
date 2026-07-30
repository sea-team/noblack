# sensitive-word-go 词库来源

本目录保存用于生成 Noblack `words.json` 的上游词库快照，来源为：

https://github.com/trustedinster/sensitive-word-go

当前导入文件：

- `sensitive_word_dict.txt`：黑名单词和数字分类；
- `sensitive_word_tags.txt`：词条标签；
- `sensitive_word_allow.txt`：白名单；
- `sensitive_word_deny.txt`：用户黑名单。

重新生成合并词库：

```bash
go run ./cmd/merge-word-library \
  -dict ./data/sensitive-word-go/sensitive_word_dict.txt \
  -tags ./data/sensitive-word-go/sensitive_word_tags.txt \
  -allow ./data/sensitive-word-go/sensitive_word_allow.txt \
  -deny ./data/sensitive-word-go/sensitive_word_deny.txt \
  -base ./words.json \
  -output ./words.json
```

词库数据和代码的许可证/来源可能不同，发布前应按上游项目许可证和数据来源单独核查。
