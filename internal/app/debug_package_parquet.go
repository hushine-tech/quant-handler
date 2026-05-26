package app

import (
	"bytes"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	"github.com/parquet-go/parquet-go"
)

type debugPackageKlineRow struct {
	TimestampMS int64   `parquet:"timestamp_ms"`
	Open        float64 `parquet:"open"`
	High        float64 `parquet:"high"`
	Low         float64 `parquet:"low"`
	Close       float64 `parquet:"close"`
	Volume      float64 `parquet:"volume"`
}

func encodeDebugPackageKlinesParquet(rows []*mdv1.MarketDataKline) ([]byte, error) {
	out := make([]debugPackageKlineRow, 0, len(rows))
	for _, row := range rows {
		if row.GetOpenTime() == nil {
			continue
		}
		out = append(out, debugPackageKlineRow{
			TimestampMS: row.GetOpenTime().AsTime().UTC().UnixMilli(),
			Open:        row.GetOpen(),
			High:        row.GetHigh(),
			Low:         row.GetLow(),
			Close:       row.GetClose(),
			Volume:      row.GetVolume(),
		})
	}
	var buf bytes.Buffer
	if err := parquet.Write(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
