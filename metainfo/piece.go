package metainfo

type Piece struct {
	Info *Info
	i    pieceIndex
}

type pieceIndex = int

func (p Piece) Length() int64 {
	if uint64(p.i) == p.Info.NumPieces()-1 {
		return p.Info.TotalLength() - int64(p.i)*p.Info.PieceLength
	}
	return p.Info.PieceLength
}

func (p Piece) Offset() int64 {
	return int64(p.i) * p.Info.PieceLength
}

func (p Piece) Hash() (ret Hash) {
	if len(p.Info.Pieces) == 0 {
		return ret
	}

	copy(ret[:], p.Info.Pieces[p.i*HashSize:(p.i+1)*HashSize])
	return
}

func (p Piece) Index() pieceIndex {
	return p.i
}
