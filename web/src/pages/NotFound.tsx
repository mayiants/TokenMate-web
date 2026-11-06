import { Button, Empty, Typography } from '@douyinfe/semi-ui';
import { useI18n } from '../i18n';

const { Title } = Typography;

export default function NotFound() {
  const { t } = useI18n();
  return (
    <Empty
      style={{ paddingTop: 96 }}
      title={<Title heading={3}>{t('notfound.title')}</Title>}
    >
      <Button type="primary" onClick={() => (location.href = '/')}>
        {t('notfound.back')}
      </Button>
    </Empty>
  );
}
