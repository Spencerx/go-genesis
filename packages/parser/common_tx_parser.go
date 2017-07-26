// Copyright 2016 The go-daylight Authors
// This file is part of the go-daylight library.
//
// The go-daylight library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-daylight library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-daylight library. If not, see <http://www.gnu.org/licenses/>.

package parser

import (
	"errors"
	"fmt"
	"time"

	"github.com/EGaaS/go-egaas-mvp/packages/consts"
	"github.com/EGaaS/go-egaas-mvp/packages/utils"
	"github.com/EGaaS/go-egaas-mvp/packages/utils/tx"

	"github.com/EGaaS/go-egaas-mvp/packages/converter"
	"github.com/EGaaS/go-egaas-mvp/packages/logging"
	"github.com/EGaaS/go-egaas-mvp/packages/utils/sql"
)

// TxParser writes transactions into the queue
func (p *Parser) TxParser(hash, binaryTx []byte, myTx bool) error {
	var err error
	var fatalError string
	var header *tx.Header
	hashHex := converter.BinToHex(hash)
	txType, walletID, citizenID := sql.GetTxTypeAndUserID(binaryTx)
	/*	if txType > 127 || consts.IsStruct(int(txType)) {
		if walletID == 0 && citizenID == 0 {
			fatalError = "undefined walletId and citizenId"
		}
	}*/
	p.BinaryData = binaryTx
	p.TxBinaryData = binaryTx
	header, err = p.ParseDataGate(false)

	if err != nil || len(fatalError) > 0 {
		p.DeleteQueueTx(hashHex) // удалим тр-ию из очереди
		// remove transaction from the turn
	}
	if err == nil && len(fatalError) > 0 {
		err = errors.New(fatalError)
	}

	if err != nil {
		log.Error("err: %v", err)
		errText := fmt.Sprintf("%s", err)
		if len(errText) > 255 {
			errText = errText[:255]
		}
		fromGate, err := p.Single("SELECT from_gate FROM queue_tx WHERE hex(hash) = ?", hashHex).Int64()
		if err != nil {
			return utils.ErrInfo(err)
		}
		log.Debug("fromGate %d", fromGate)
		if fromGate == 0 {
			log.Debug("UPDATE transactions_status SET error = %s WHERE hex(hash) = %s", errText, hashHex)
			err = p.ExecSQL("UPDATE transactions_status SET error = ? WHERE hex(hash) = ?", errText, hashHex)
			if err != nil {
				return utils.ErrInfo(err)
			}
		}
	} else {
		if !( /*txType > 127 ||*/ consts.IsStruct(int(txType))) {
			if header == nil {
				return utils.ErrInfo(errors.New("header is nil"))
			}
			walletID = header.StateID
			citizenID = header.UserID
		}

		log.Debug("SELECT counter FROM transactions WHERE hex(hash) = ?", string(hashHex))
		logging.WriteSelectiveLog("SELECT counter FROM transactions WHERE hex(hash) = " + string(hashHex))
		counter, err := p.Single("SELECT counter FROM transactions WHERE hex(hash) = ?", hashHex).Int64()
		if err != nil {
			logging.WriteSelectiveLog(err)
			return utils.ErrInfo(err)
		}
		logging.WriteSelectiveLog("counter: " + converter.Int64ToStr(counter))
		counter++
		logging.WriteSelectiveLog("DELETE FROM transactions WHERE hex(hash) = " + string(hashHex))
		affect, err := p.ExecSQLGetAffect(`DELETE FROM transactions WHERE hex(hash) = ?`, hashHex)
		if err != nil {
			logging.WriteSelectiveLog(err)
			return utils.ErrInfo(err)
		}
		logging.WriteSelectiveLog("affect: " + converter.Int64ToStr(affect))

		log.Debug("INSERT INTO transactions (hash, data, for_self_use, type, wallet_id, citizen_id, third_var, counter) VALUES (%s, %s, %v, %v, %v, %v, %v, %v)", hashHex, converter.BinToHex(binaryTx), 0, txType, walletID, citizenID, 0, counter)
		logging.WriteSelectiveLog("INSERT INTO transactions (hash, data, for_self_use, type, wallet_id, citizen_id, third_var, counter) VALUES ([hex], [hex], ?, ?, ?, ?, ?, ?)")
		// вставляем с verified=1
		// put with verified=1
		err = p.ExecSQL(`INSERT INTO transactions (hash, data, for_self_use, type, wallet_id, citizen_id, third_var, counter, verified) VALUES ([hex], [hex], ?, ?, ?, ?, ?, ?, 1)`, hashHex, converter.BinToHex(binaryTx), 0, txType, walletID, citizenID, 0, counter)
		if err != nil {
			logging.WriteSelectiveLog(err)
			return utils.ErrInfo(err)
		}
		logging.WriteSelectiveLog("result insert")
		log.Debug("INSERT INTO transactions - OK")
		// удалим тр-ию из очереди (с verified=0)
		// remove transaction from the turn (with verified=0)
		err = p.DeleteQueueTx(hashHex)
		if err != nil {
			return utils.ErrInfo(err)
		}
	}
	return nil
}

// DeleteQueueTx deletes a transaction from the queue
func (p *Parser) DeleteQueueTx(hashHex []byte) error {
	log.Debug("DELETE FROM queue_tx WHERE hex(hash) = %s", hashHex)
	err := p.ExecSQL("DELETE FROM queue_tx WHERE hex(hash) = ?", hashHex)
	if err != nil {
		return utils.ErrInfo(err)
	}
	// т.к. мы обрабатываем в queue_parser_tx тр-ии с verified=0, то после их обработки их нужно удалять.
	// Because we process transactions with verified=0 in queue_parser_tx, after processing we need to delete them
	logging.WriteSelectiveLog("DELETE FROM transactions WHERE hex(hash) = " + string(hashHex) + " AND verified=0 AND used = 0")
	affect, err := p.ExecSQLGetAffect("DELETE FROM transactions WHERE hex(hash) = ? AND verified=0 AND used = 0", hashHex)
	if err != nil {
		logging.WriteSelectiveLog(err)
		return utils.ErrInfo(err)
	}
	logging.WriteSelectiveLog("affect: " + converter.Int64ToStr(affect))
	return nil
}

// AllTxParser parses new transactions
func (p *Parser) AllTxParser() error {

	// берем тр-ии
	// take the transactions
	all, err := p.GetAll(`
			SELECT *
			FROM (
	              SELECT data,
	                         hash
	              FROM queue_tx
				UNION
				SELECT data,
							 hash
				FROM transactions
				WHERE verified = 0 AND
							 used = 0
			)  AS x
			`, -1)
	for _, data := range all {

		log.Debug("hash: %x", data["hash"])

		err = p.TxParser([]byte(data["hash"]), []byte(data["data"]), false)
		if err != nil {
			err0 := p.ExecSQL(`INSERT INTO incorrect_tx (time, hash, err) VALUES (?, [hex], ?)`, time.Now().Unix(), converter.BinToHex(data["hash"]), fmt.Sprintf("%s", err))
			if err0 != nil {
				log.Error("%v", utils.ErrInfo(err0))
			}
			return utils.ErrInfo(err)
		}
	}
	return nil
}
