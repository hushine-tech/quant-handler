package app

import (
	"bytes"
	"io"

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
	var buf bytes.Buffer
	writer := parquet.NewGenericWriter[debugPackageKlineRow](
		&buf,
		parquet.MaxRowsPerRowGroup(10_000),
	)
	batch := make([]debugPackageKlineRow, 0, 1024)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		written, err := writer.Write(batch)
		if err != nil {
			return err
		}
		if written != len(batch) {
			return io.ErrShortWrite
		}
		batch = batch[:0]
		return nil
	}
	for _, row := range rows {
		if row.GetOpenTime() == nil {
			continue
		}
		batch = append(batch, debugPackageKlineRow{
			TimestampMS: row.GetOpenTime().AsTime().UTC().UnixMilli(),
			Open:        row.GetOpen(),
			High:        row.GetHigh(),
			Low:         row.GetLow(),
			Close:       row.GetClose(),
			Volume:      row.GetVolume(),
		})
		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				_ = writer.Close()
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
