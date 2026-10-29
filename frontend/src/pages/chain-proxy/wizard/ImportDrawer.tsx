import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Button, Drawer, Input, Space, Switch, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { getMessage } from '@/utils/messageBus';
import { useChainMutations } from '@/api/queries/useChain';
import type { ChainImportResult } from '@/schemas/chain';

interface Props {
  open: boolean;
  poolId: number;
  poolName: string;
  onClose: () => void;
}

type PreviewRow = { key: string; lineNo: number; text: string; error?: string };

export default function ImportDrawer({ open, poolId, poolName, onClose }: Props) {
  const { t } = useTranslation();
  const { importProxies, importing } = useChainMutations();
  const [content, setContent] = useState('');
  const [allowPrivate, setAllowPrivate] = useState(false);
  const [result, setResult] = useState<ChainImportResult | null>(null);

  const rows = useMemo<PreviewRow[]>(() => {
    if (!result) return [];
    const ok = result.preview.map((p) => ({
      key: `ok-${p.lineNo}`,
      lineNo: p.lineNo,
      text: p.proxy ?? p.line,
    }));
    const bad = (result.invalid ?? []).map((p) => ({
      key: `bad-${p.lineNo}`,
      lineNo: p.lineNo,
      text: p.line,
      error: p.error,
    }));
    return [...ok, ...bad].sort((a, b) => a.lineNo - b.lineNo);
  }, [result]);

  const columns: ColumnsType<PreviewRow> = [
    { title: '#', dataIndex: 'lineNo', width: 64 },
    {
      title: t('pages.chainProxy.import.line'),
      dataIndex: 'text',
      ellipsis: true,
      render: (value: string) => <span className="chain-mono">{value}</span>,
    },
    {
      title: t('pages.chainProxy.import.state'),
      dataIndex: 'error',
      width: 140,
      render: (error?: string) =>
        error ? (
          <Tag color="red">{error}</Tag>
        ) : (
          <Tag color="green">{t('pages.chainProxy.import.ok')}</Tag>
        ),
    },
  ];

  const reset = () => {
    setContent('');
    setResult(null);
    setAllowPrivate(false);
  };

  const close = () => {
    reset();
    onClose();
  };

  const preview = async () => {
    if (!content.trim()) return;
    try {
      const res = await importProxies({ poolId, content, dryRun: true, allowPrivate });
      setResult(res);
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  };

  const submit = async () => {
    if (!content.trim()) return;
    try {
      const res = await importProxies({ poolId, content, dryRun: false, allowPrivate });
      getMessage().success(
        t('pages.chainProxy.import.done', { created: res.created, duplicate: res.duplicate }),
      );
      close();
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  };

  return (
    <Drawer
      open={open}
      onClose={close}
      width={720}
      title={t('pages.chainProxy.import.title', { name: poolName })}
      destroyOnHidden
      footer={
        <Space style={{ justifyContent: 'flex-end', width: '100%' }}>
          <Button onClick={close}>{t('cancel')}</Button>
          <Button onClick={preview} loading={importing}>
            {t('pages.chainProxy.import.preview')}
          </Button>
          <Button type="primary" onClick={submit} loading={importing} disabled={!result}>
            {t('pages.chainProxy.import.submit')}
          </Button>
        </Space>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Alert type="info" showIcon message={t('pages.chainProxy.import.hint')} />
        <Input.TextArea
          aria-label={t('pages.chainProxy.import.title', { name: poolName })}
          value={content}
          onChange={(event) => setContent(event.target.value)}
          rows={8}
          spellCheck={false}
          className="chain-mono"
          placeholder={
            'socks5://user:pass@1.2.3.4:1080\n1.2.3.4:1080:user:pass\nsocks5://5.6.7.8:1080'
          }
        />
        <Space>
          <Switch
            aria-label={t('pages.chainProxy.import.allowPrivate')}
            checked={allowPrivate}
            onChange={setAllowPrivate}
          />
          <Typography.Text>{t('pages.chainProxy.import.allowPrivate')}</Typography.Text>
        </Space>
        {result ? (
          <>
            <Space size={12} wrap>
              <Tag color="blue">
                {t('pages.chainProxy.import.valid')}: {result.valid}
              </Tag>
              <Tag color="orange">
                {t('pages.chainProxy.import.duplicate')}: {result.duplicate}
              </Tag>
              <Tag color="red">
                {t('pages.chainProxy.import.invalid')}: {result.invalid?.length ?? 0}
              </Tag>
              {!result.dryRun ? (
                <Tag color="green">
                  {t('pages.chainProxy.import.created')}: {result.created}
                </Tag>
              ) : null}
            </Space>
            <Table<PreviewRow>
              size="small"
              columns={columns}
              dataSource={rows}
              pagination={{ pageSize: 10, showSizeChanger: false }}
              scroll={{ y: 260 }}
            />
          </>
        ) : null}
      </Space>
    </Drawer>
  );
}
