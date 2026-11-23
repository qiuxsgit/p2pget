package link

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	goed2k "github.com/goed2k/core"
)

type Request struct {
	Kind, ID, Name, Raw string
	Size                int64
	Spec                *torrent.TorrentSpec
	EDLink              goed2k.EMuleLink
}

func Parse(value string) (Request, error) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	if strings.HasPrefix(value, "magnet:?") {
		spec, err := torrent.TorrentSpecFromMagnetUri(value)
		if err != nil {
			return Request{}, fmt.Errorf("无效的磁力链接：%w", err)
		}
		r, err := FromTorrentSpec(spec, value)
		return r, err
	}
	if strings.HasPrefix(value, "ed2k://") {
		parts := strings.Split(value, "|")
		if len(parts) < 6 || parts[1] != "file" {
			return Request{}, fmt.Errorf("需要 ED2K 文件链接")
		}
		name, err := url.PathUnescape(parts[2])
		if err != nil {
			return Request{}, err
		}
		if err = ValidateFileName(name); err != nil {
			return Request{}, err
		}
		size, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil || size <= 0 {
			return Request{}, fmt.Errorf("ED2K 文件大小必须是正整数")
		}
		hash, err := hex.DecodeString(parts[4])
		if err != nil || len(hash) != 16 {
			return Request{}, fmt.Errorf("ED2K 哈希必须是 32 位十六进制")
		}
		link, err := goed2k.ParseEMuleLink(value)
		if err != nil {
			return Request{}, fmt.Errorf("无效的 ED2K 链接：%w", err)
		}
		// Retain the once-decoded name: the library parser decodes names twice.
		link.StringValue = name
		return Request{Kind: "ed2k", ID: strings.ToLower(parts[4]), Name: name, Raw: value, Size: size, EDLink: link}, nil
	}
	if strings.EqualFold(filepath.Ext(value), ".torrent") {
		file, err := os.Open(value)
		if err != nil {
			return Request{}, err
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil {
			return Request{}, err
		}
		if !stat.Mode().IsRegular() || stat.Size() > 32<<20 {
			return Request{}, fmt.Errorf("种子文件过大或不是普通文件")
		}
		mi, err := metainfo.Load(io.LimitReader(file, 32<<20))
		if err != nil {
			return Request{}, fmt.Errorf("读取种子失败：%w", err)
		}
		spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
		if err != nil {
			return Request{}, err
		}
		info, err := mi.UnmarshalInfo()
		if err != nil {
			return Request{}, err
		}
		if err = ValidateTorrentInfo(&info); err != nil {
			return Request{}, err
		}
		r, err := FromTorrentSpec(spec, value)
		r.Size = info.TotalLength()
		return r, err
	}
	return Request{}, fmt.Errorf("请输入 magnet 链接、ed2k 文件链接或本地 .torrent 文件路径")
}

func FromTorrentSpec(spec *torrent.TorrentSpec, raw string) (Request, error) {
	id := ""
	if spec.InfoHash != (metainfo.Hash{}) {
		id = spec.InfoHash.HexString()
	} else if spec.InfoHashV2.Ok {
		id = hex.EncodeToString(spec.InfoHashV2.Value[:])
	}
	if id == "" {
		return Request{}, fmt.Errorf("磁力链接缺少有效的 BT 哈希")
	}
	return Request{Kind: "bt", ID: id, Name: spec.DisplayName, Raw: raw, Spec: spec}, nil
}

func ValidateFileName(name string) error {
	if strings.EqualFold(name, ".p2pget") {
		return fmt.Errorf("文件名与程序状态目录冲突")
	}
	if name == "" || name == "." || name == ".." || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("不支持的文件名 %q", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`/\<>:"|?*`, r) {
			return fmt.Errorf("文件名含非法路径字符：%q", name)
		}
	}
	base := strings.ToUpper(strings.Split(name, ".")[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
		return fmt.Errorf("文件名 %q 在 Windows 上不可用", name)
	}
	return nil
}

func ValidateTorrentInfo(info *metainfo.Info) error {
	if err := ValidateFileName(info.BestName()); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, file := range info.UpvertedFiles() {
		parts := file.BestPath()
		for _, part := range parts {
			if err := ValidateFileName(part); err != nil {
				return err
			}
		}
		key := norm.NFC.String(strings.ToLower(strings.Join(parts, "/")))
		if seen[key] {
			return fmt.Errorf("种子含有大小写或 Unicode 等价的重复路径：%s", key)
		}
		for existing := range seen {
			if strings.HasPrefix(key, existing+"/") || strings.HasPrefix(existing, key+"/") {
				return fmt.Errorf("种子文件与目录路径冲突：%s", key)
			}
		}
		seen[key] = true
	}
	return nil
}
